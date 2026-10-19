package ctl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readSecretFromTerminal reads a line without echoing it. It uses stty with fixed arguments
// (no shell); when stdin is not a terminal it simply reads a line.
func readSecretFromTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if stdinIsTerminal() {
		off := exec.Command("stty", "-echo")
		off.Stdin = os.Stdin
		if err := off.Run(); err != nil {
			// Never read a secret while it would be echoed on the screen.
			fmt.Fprintln(os.Stderr)
			return "", fmt.Errorf("cannot turn off terminal echo (is stty available?); use --client-secret-file instead")
		}
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			_ = on.Run()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// reader returns the shared line reader on a.In, so consecutive prompts do not lose input.
func (a *App) reader() *bufio.Reader {
	if a.inReader == nil {
		a.inReader = bufio.NewReader(a.In)
	}
	return a.inReader
}

// ask prints a prompt (with the default in brackets) and returns the answer.
func (a *App) ask(label, def string) string {
	if def != "" {
		fmt.Fprintf(a.Out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(a.Out, "%s: ", label)
	}
	line, _ := a.reader().ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// askSecret reads a secret without echo. keep is shown as the "press Enter to keep" hint.
func (a *App) askSecret(label string, haveExisting bool) (string, error) {
	prompt := label
	if haveExisting {
		prompt += " (Enter keeps the current value)"
	}
	prompt += ": "
	if a.ReadSecret != nil {
		return a.ReadSecret(prompt)
	}
	fmt.Fprint(a.Err, prompt)
	line, err := a.reader().ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// confirm asks a yes/no question. yes (the --yes flag) answers for the user; without a
// terminal and without --yes the answer is no, so scripts never proceed by accident.
func (a *App) confirm(_ context.Context, question string, yes bool) bool {
	if yes {
		return true
	}
	if a.Interactive == nil || !a.Interactive() {
		fmt.Fprintf(a.Err, "%s\n  (not a terminal: pass --yes to confirm)\n", question)
		return false
	}
	fmt.Fprintf(a.Out, "%s [y/N]: ", question)
	line, _ := a.reader().ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
