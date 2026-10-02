package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

type recordedCall struct {
	method string
	params any
}

type fakeExecutor struct {
	calls     []recordedCall
	worldID   runtime.ExecutionContextID
	value     string
	createErr error
}

func (f *fakeExecutor) Execute(_ context.Context, method string, params, res any) error {
	f.calls = append(f.calls, recordedCall{method: method, params: params})
	switch out := res.(type) {
	case *cdppage.CreateIsolatedWorldReturns:
		if f.createErr != nil {
			return f.createErr
		}
		out.ExecutionContextID = f.worldID
	case *runtime.EvaluateReturns:
		out.Result = &runtime.RemoteObject{Value: []byte(f.value)}
	default:
		return fmt.Errorf("unexpected result type %T", res)
	}
	return nil
}

func TestScriptsRunInAnIsolatedWorldOfTheMainFrame(t *testing.T) {
	fake := &fakeExecutor{worldID: 7, value: `{"ok":true}`}
	ctx := cdp.WithExecutor(context.Background(), fake)

	value, err := evaluateInWorld(ctx, "frame-1", "1+1", false)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(value))

	require.Len(t, fake.calls, 2)
	assert.Equal(t, cdppage.CommandCreateIsolatedWorld, fake.calls[0].method)
	create, ok := fake.calls[0].params.(*cdppage.CreateIsolatedWorldParams)
	require.True(t, ok)
	assert.Equal(t, cdp.FrameID("frame-1"), create.FrameID)
	assert.Equal(t, isolatedWorldName, create.WorldName)
	assert.False(t, create.GrantUniveralAccess)

	assert.Equal(t, runtime.CommandEvaluate, fake.calls[1].method)
	eval, ok := fake.calls[1].params.(*runtime.EvaluateParams)
	require.True(t, ok)
	assert.Equal(t, int64(7), int64(eval.ContextID), "an evaluation without a context id would run in the page's own world")
}

func TestNoScriptRunsWhenTheIsolatedWorldCannotBeCreated(t *testing.T) {
	cause := &fakeProtocolError{}
	fake := &fakeExecutor{createErr: cause}
	ctx := cdp.WithExecutor(context.Background(), fake)

	_, err := evaluateInWorld(ctx, "frame-1", "1+1", false)
	require.ErrorIs(t, err, cause)
	assert.Len(t, fake.calls, 1)
}

type fakeProtocolError struct{}

func (*fakeProtocolError) Error() string { return "no frame for the given id" }

func scriptReturning(value string, err error) func(context.Context, string, bool) (json.RawMessage, error) {
	return func(context.Context, string, bool) (json.RawMessage, error) {
		return json.RawMessage(value), err
	}
}

func TestFocusMustStayOnTheObservedField(t *testing.T) {
	t.Run("it stayed", func(t *testing.T) {
		s := newTestSession(t)
		s.script = scriptReturning("true", nil)
		assert.NoError(t, s.confirmFocus(context.Background(), 3))
	})
	t.Run("it moved elsewhere or the field became a password", func(t *testing.T) {
		s := newTestSession(t)
		s.script = scriptReturning("false", nil)
		err := s.confirmFocus(context.Background(), 3)
		assert.ErrorIs(t, err, page.ErrOutcomeUnknown)
	})
	t.Run("the check itself failed", func(t *testing.T) {
		s := newTestSession(t)
		cause := errors.New("page went away")
		s.script = scriptReturning("", cause)
		err := s.confirmFocus(context.Background(), 3)
		assert.ErrorIs(t, err, page.ErrOutcomeUnknown)
		assert.ErrorIs(t, err, cause)
	})
	t.Run("a cancelled caller is not an unknown outcome", func(t *testing.T) {
		s := newTestSession(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.script = scriptReturning("", ctx.Err())
		err := s.confirmFocus(ctx, 3)
		assert.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, page.ErrOutcomeUnknown)
	})
}

func TestFocusScriptNamesTheObservedNodeAndTheSensitiveTypes(t *testing.T) {
	script := fmt.Sprintf(focusScript, 42)
	assert.Contains(t, script, "nodes.get(42)")
	assert.Contains(t, script, `['password','file','hidden']`)
	assert.Contains(t, script, "active===e")
}
