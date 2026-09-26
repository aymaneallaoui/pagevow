package page

import "errors"

// ErrStalePage reports that the page changed since it was observed; nothing was executed.
var ErrStalePage = errors.New("page changed since it was observed")

// ErrTargetRefused reports that the target is gone, covered or disabled; nothing was executed.
var ErrTargetRefused = errors.New("target changed or is covered")

// ErrSelectUnconfirmed reports that a dropdown change could not be confirmed and must not be repeated blindly.
var ErrSelectUnconfirmed = errors.New("dropdown execution was not confirmed")
