package browser

import (
	"context"
	"errors"
	"fmt"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Capture returns a PNG or JPEG screenshot of the page or, with fullPage, of its whole scrollable area; it gives up after 5 seconds.
func (s *Session) Capture(ctx context.Context, format string, fullPage bool) ([]byte, error) {
	screenshotFormat, err := screenshotFormatOf(format)
	if err != nil {
		return nil, err
	}
	bound, cancel := s.bind(ctx)
	defer cancel()
	timed, stopTimer := context.WithTimeout(bound, captureTimeout)
	defer stopTimer()

	var data []byte
	err = chromedp.Run(timed, chromedp.ActionFunc(func(ctx context.Context) error {
		params := cdppage.CaptureScreenshot().WithFormat(screenshotFormat)
		if fullPage {
			_, _, _, _, _, size, err := cdppage.GetLayoutMetrics().Do(ctx)
			if err != nil {
				return fmt.Errorf("read layout metrics: %w", err)
			}
			clip := &cdppage.Viewport{X: 0, Y: 0, Width: size.Width, Height: size.Height, Scale: 1}
			params = params.WithCaptureBeyondViewport(true).WithClip(clip)
		}
		var err error
		data, err = params.Do(ctx)
		return err
	}))
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("capture screenshot: %w", cerr)
		}
		if errors.Is(timed.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("capture screenshot: no result within %s: %w", captureTimeout, context.DeadlineExceeded)
		}
		return nil, fmt.Errorf("capture screenshot: %w", s.interrupted(ctx, err))
	}
	return data, nil
}

func screenshotFormatOf(format string) (cdppage.CaptureScreenshotFormat, error) {
	switch format {
	case captureFormatPNG:
		return cdppage.CaptureScreenshotFormatPng, nil
	case captureFormatJPEG, "jpg":
		return cdppage.CaptureScreenshotFormatJpeg, nil
	default:
		return "", fmt.Errorf("capture screenshot: unsupported format %q (use png or jpeg)", format)
	}
}

// Close closes this session's own tab and releases its connection.
func (s *Session) Close(ctx context.Context) error {
	bound, cancel := s.bind(ctx)
	defer cancel()
	err := chromedp.Cancel(bound)
	s.cancel()
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	return nil
}
