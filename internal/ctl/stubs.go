package ctl

import "context"

func (a *App) notYet(name string) int {
	return a.fail(ExitFail, name+" is not implemented yet", "")
}

func (a *App) cmdDoctor(ctx context.Context, args []string) int   { return a.notYet("doctor") }
func (a *App) cmdCheck(ctx context.Context, args []string) int    { return a.notYet("check") }
func (a *App) cmdUpdate(ctx context.Context, args []string) int   { return a.notYet("update") }
func (a *App) cmdRollback(ctx context.Context, args []string) int { return a.notYet("rollback") }
func (a *App) cmdSupport(ctx context.Context, args []string) int  { return a.notYet("support") }
