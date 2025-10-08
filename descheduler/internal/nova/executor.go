// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/cobaltcore-dev/cortex/descheduler/internal/conf"
	"github.com/sapcc/go-bits/jobloop"
)

type Executor interface {
	// Deschedule the vm ids provided.
	Deschedule(ctx context.Context, vmIDs []string) error
}

type executor struct {
	// Nova API to execute the descheduling operations.
	novaAPI NovaAPI
	// Configuration for the descheduler.
	config conf.DeschedulerConfig
	// Monitor for tracking the descheduler execution.
	monitor Monitor
}

// Create a new executor for the Nova descheduler.
func NewExecutor(novaAPI NovaAPI, m Monitor, config conf.DeschedulerConfig) Executor {
	return &executor{novaAPI: novaAPI, monitor: m, config: config}
}

type descheduling struct {
	// If the descheduling errored.
	err error
	// If the descheduling was skipped.
	skipped bool
	// The previous host of the VM.
	source string
	// The target host after migration.
	target string
	// The VM ID that was (attempted to be) descheduled.
	vmId string
}

// Execute the virtual machine live-migrations using the Nova API.
func (e *executor) Deschedule(ctx context.Context, vmIds []string) error {
	errs := make([]error, 0, len(vmIds))
	for _, vmId := range vmIds {
		t := time.Now()
		result := e.descheduleVM(ctx, vmId)
		if e.monitor.deschedulingRunTimer != nil {
			labels := []string{
				strconv.FormatBool(result.err != nil),
				strconv.FormatBool(result.skipped),
				result.source, result.target, result.vmId,
			}
			e.monitor.deschedulingRunTimer.
				WithLabelValues(labels...).
				Observe(time.Since(t).Seconds())
		}
		if result.err != nil {
			slog.Error(
				"descheduler: failed to deschedule VM",
				"vmId", vmId, "error", result.err,
			)
			errs = append(errs, result.err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Live-migrate a virtual machine to a different host.
func (e *executor) descheduleVM(ctx context.Context, vmId string) descheduling {
	server, err := e.novaAPI.Get(ctx, vmId)
	if err != nil {
		slog.Error("descheduler: failed to get VM details", "vmId", vmId, "error", err)
		return descheduling{err: err, vmId: vmId}
	}
	hostBefore := server.ComputeHost
	if server.Status != "ACTIVE" {
		slog.Info(
			"descheduler: VM not active, skipping migration",
			"vmId", vmId, "status", server.Status,
		)
		return descheduling{skipped: true, vmId: vmId, source: hostBefore, target: hostBefore}
	}
	slog.Info(
		"descheduler: live-migrating virtual machine",
		"vmId", vmId, "host", server.ComputeHost,
	)
	if !e.config.Nova.DisableDryRun {
		slog.Info("descheduler: dry-run enabled, skipping execution", "vmId", vmId)
		return descheduling{skipped: true, vmId: vmId, source: hostBefore, target: hostBefore}
	}
	slog.Info("descheduler: executing migration for VM", "vmId", vmId)
	if err := e.novaAPI.LiveMigrate(ctx, vmId); err != nil {
		slog.Error("descheduler: failed to live-migrate VM", "vmId", vmId, "error", err)
		return descheduling{err: err, vmId: vmId, source: hostBefore, target: hostBefore}
	}
	// Wait for the migration to complete.
	slog.Info("descheduler: live-migration started", "vmId", vmId)
	for {
		server, err = e.novaAPI.Get(ctx, vmId)
		if err != nil {
			slog.Error("descheduler: failed to get VM status", "vmId", vmId, "error", err)
			// Consider migration as failed
			return descheduling{err: err, vmId: vmId, source: hostBefore, target: hostBefore}
		}
		if server.Status == "ACTIVE" {
			slog.Info(
				"descheduler: live-migration completed, VM now runs on new host",
				"vmId", vmId, "host", server.ComputeHost,
			)
			break
		}
		if server.Status == "ERROR" {
			slog.Error("descheduler: live-migration failed", "vmId", vmId)
			return descheduling{
				err:  errors.New("live-migration failed for VM " + vmId),
				vmId: vmId, source: hostBefore, target: server.ComputeHost,
			}
		}
		slog.Info(
			"descheduler: waiting for live-migration to complete",
			"vmId", vmId, "status", server.Status,
		)
		time.Sleep(jobloop.DefaultJitter(time.Second))
	}
	return descheduling{vmId: vmId, source: hostBefore, target: server.ComputeHost}
}
