package cli

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"
)

// Prompter asks the user for values on a terminal.
type Prompter interface {
	Input(title, placeholder string) (string, error)
	Secret(title string) (string, error)
}

type huhPrompter struct{}

func (huhPrompter) Input(title, placeholder string) (string, error) {
	var value string
	err := huh.NewInput().Title(title).Placeholder(placeholder).Value(&value).Run()
	return value, promptError(err)
}

func (huhPrompter) Secret(title string) (string, error) {
	var value string
	err := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&value).Run()
	return value, promptError(err)
}

func promptError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, huh.ErrUserAborted):
		return errors.New("cancelled")
	default:
		return fmt.Errorf("prompt: %w", err)
	}
}
