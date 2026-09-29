package browser

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/chromedp/cdproto"
	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/inspector"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	dialogMessageLimit = 300
	closePollPeriod    = 20 * time.Millisecond
)

// DialogEvent records one JavaScript dialog that the session accepted.
type DialogEvent struct {
	// Type is alert, confirm, prompt or beforeunload.
	Type    string
	Message string
	// ActionID is the action that was executing when the dialog opened, empty when none was.
	ActionID string
	Time     time.Time
}

// PopupEvent records one tab that the page opened; the session closes it with itself.
type PopupEvent struct {
	URL  string
	Time time.Time
}

// DownloadEvent records one download that the page started.
type DownloadEvent struct {
	URL               string
	SuggestedFilename string
	// Denied is true when the download was refused because the session has no download directory.
	Denied bool
	Time   time.Time
}

// Dialogs returns the dialogs the session accepted, oldest first.
func (s *Session) Dialogs() []DialogEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.dialogs)
}

// Downloads returns the downloads the page started, oldest first.
func (s *Session) Downloads() []DownloadEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.downloads)
}

// Popups returns the tabs the page opened, oldest first.
func (s *Session) Popups() []PopupEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.popups)
}

func (s *Session) listen(ctx context.Context) {
	listenCtx, stop := context.WithCancel(ctx)
	s.stopListening = stop
	chromedp.ListenTarget(listenCtx, s.onTargetEvent)
	chromedp.ListenBrowser(listenCtx, s.onBrowserEvent)
}

// The listeners run on the chromedp event goroutines, so they only record and hand slow work to a goroutine.
func (s *Session) onTargetEvent(ev any) {
	switch ev := ev.(type) {
	case *cdppage.EventJavascriptDialogOpening:
		s.dialogOpened(ev)
	case *inspector.EventTargetCrashed:
		s.targetGone()
	case *target.EventTargetCrashed:
		if ev.TargetID == s.targetID {
			s.targetGone()
		}
	case *target.EventTargetDestroyed:
		if ev.TargetID == s.targetID {
			s.targetGone()
		}
	case *target.EventTargetCreated:
		s.popupSeen(ev.TargetInfo)
	case *target.EventTargetInfoChanged:
		s.popupSeen(ev.TargetInfo)
	case *cdppage.EventFrameAttached:
		s.addFrame(ev.FrameID)
	case *cdppage.EventFrameNavigated:
		if ev.Frame != nil {
			s.addFrame(ev.Frame.ID)
		}
	}
}

func (s *Session) onBrowserEvent(ev any) {
	if ev, ok := ev.(*cdpbrowser.EventDownloadWillBegin); ok {
		s.downloadStarted(ev)
	}
}

func (s *Session) targetGone() {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if !closing {
		s.markCrashed()
	}
}

func (s *Session) dialogOpened(ev *cdppage.EventJavascriptDialogOpening) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	s.dialogs = append(s.dialogs, DialogEvent{
		Type:     ev.Type.String(),
		Message:  truncateRunes(ev.Message, dialogMessageLimit),
		ActionID: s.current,
		Time:     time.Now(),
	})
	s.handlers.Add(1)
	go s.acceptDialog(ev.DefaultPrompt)
}

func (s *Session) acceptDialog(defaultPrompt string) {
	defer s.handlers.Done()
	ctx, cancel := context.WithTimeout(s.ctx, s.callTimeout)
	defer cancel()
	_ = chromedp.Run(ctx, cdppage.HandleJavaScriptDialog(true).WithPromptText(defaultPrompt))
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func (s *Session) addFrame(id cdp.FrameID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames[id] = struct{}{}
}

func (s *Session) downloadStarted(ev *cdpbrowser.EventDownloadWillBegin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, own := s.frames[ev.FrameID]; !own {
		return
	}
	s.downloads = append(s.downloads, DownloadEvent{
		URL:               ev.URL,
		SuggestedFilename: ev.SuggestedFilename,
		Denied:            !s.downloadsAllowed,
		Time:              time.Now(),
	})
}

func (s *Session) popupSeen(info *target.Info) {
	if info == nil || info.Type != "page" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if index, known := s.popupIndex[info.TargetID]; known {
		if info.URL != "" {
			s.popups[index].URL = info.URL
		}
		return
	}
	_, openedByPopup := s.popupIndex[info.OpenerID]
	if info.OpenerID != s.targetID && !openedByPopup {
		return
	}
	s.popupIndex[info.TargetID] = len(s.popups)
	s.popups = append(s.popups, PopupEvent{URL: info.URL, Time: time.Now()})
	s.frames[cdp.FrameID(info.TargetID)] = struct{}{}
}

func popupTargets(infos []*target.Info, own target.ID, known []target.ID) []target.ID {
	owned := map[target.ID]bool{own: true}
	for _, id := range known {
		owned[id] = true
	}
	for grew := true; grew; {
		grew = false
		for _, info := range infos {
			if info.Type == "page" && !owned[info.TargetID] && owned[info.OpenerID] {
				owned[info.TargetID] = true
				grew = true
			}
		}
	}
	delete(owned, own)
	return slices.Sorted(maps.Keys(owned))
}

func (s *Session) listTargets(ctx context.Context) ([]*target.Info, error) {
	var infos []*target.Info
	err := withTimeout(ctx, s.callTimeout, func(ctx context.Context) error {
		var err error
		infos, err = target.GetTargets().Do(cdp.WithExecutor(ctx, s.browser))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	return infos, nil
}

// closePopups closes every tab the page opened and returns their ids.
func (s *Session) closePopups(ctx context.Context) ([]target.ID, error) {
	s.mu.Lock()
	known := slices.Collect(maps.Keys(s.popupIndex))
	s.mu.Unlock()

	infos, listErr := s.listTargets(ctx)
	errs := []error{}
	if listErr != nil {
		errs = append(errs, listErr)
	}
	ids := popupTargets(infos, s.targetID, known)
	for _, id := range ids {
		err := withTimeout(ctx, s.callTimeout, func(ctx context.Context) error {
			return target.CloseTarget(id).Do(cdp.WithExecutor(ctx, s.browser))
		})
		var protocol *cdproto.Error
		if err != nil && !errors.As(err, &protocol) {
			errs = append(errs, fmt.Errorf("close popup %s: %w", id, err))
		}
	}
	return ids, errors.Join(errs...)
}

// waitClosed returns once none of the tabs is listed any more, because the browser lists a tab briefly after it closed it.
func (s *Session) waitClosed(ctx context.Context, ids []target.ID) error {
	waitCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	ticker := time.NewTicker(closePollPeriod)
	defer ticker.Stop()
	for {
		infos, err := s.listTargets(waitCtx)
		if err != nil {
			return err
		}
		open := slices.ContainsFunc(infos, func(info *target.Info) bool { return slices.Contains(ids, info.TargetID) })
		if !open {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("tabs %v still open %s after being closed", ids, s.callTimeout)
		case <-ticker.C:
		}
	}
}
