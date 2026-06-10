// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0

// Package actions executes browser action rules without depending on crawler state.
package actions

import (
	"context"
	"errors"
	"strings"

	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// ErrRuntimeStopped signals that the runtime has been stopped and can no longer execute actions.
var ErrRuntimeStopped = errors.New("browser action runtime stopped")

// RbeeEndpoints centralizes the browser-visible Rbee/HBS endpoint configuration.
type RbeeEndpoints struct {
	Action string
}

// HBSOptions explicitly controls human-behavior simulation and Selenium fallback.
type HBSOptions struct {
	Enabled          bool
	SeleniumFallback bool
	Rbee             RbeeEndpoints
}

// Options controls action execution behavior.
type Options struct {
	HBS HBSOptions
}

// ScreenshotHook captures a screenshot requested by an action rule.
type ScreenshotHook func(ctx context.Context, filename string, maxHeight int) error

// CookieSink receives cookies collected by action post-processing.
type CookieSink interface {
	CollectCookies(context.Context, map[string]interface{}) error
}

// RuleLookup supplies the crawler-specific rule and selector operations needed by
// the generic executor.
type RuleLookup interface {
	FindElement(context.Context, rules.Selector) (vdi.WebElement, error)
	PluginScript(context.Context, string) (string, bool, error)
	CallPlugin(context.Context, string, string) error
}

// Runtime is the narrow set of dependencies required to execute action rules.
type Runtime struct {
	ContextID   string
	WebDriver   vdi.WebDriver
	Rules       RuleLookup
	Cookies     CookieSink
	CheckStatus func(context.Context) error
	Screenshot  ScreenshotHook
	Options     Options
}

func (r *Runtime) context(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (r *Runtime) check(ctx context.Context) error {
	ctx = r.context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.WebDriver == nil {
		return errors.New("browser actions: WebDriver is nil")
	}
	if r.CheckStatus != nil {
		if err := r.CheckStatus(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (r *Runtime) hbsEndpoint() string {
	return strings.TrimSpace(r.Options.HBS.Rbee.Action)
}
