package driver

import (
	"context"
	"fmt"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The run report (internal/report). The Session stays pure: the Driver
// owns a report.Collector for each real run that reaches its queue, tells
// it when steps start (their Effects) and end (their Events), and at
// Finished asks it for the report, which it adds to the events
// (engine.RunReport) and, reduced, to the last-run record. All of it runs on
// the loop, like the other Effects that touch the system, so it is bounded:
// each walk of a folder, the measuring in all, and the end (report.Config).
// A Close in progress (d.live cancelled) makes every measurement unknown at
// once, so it never holds the teardown up.

// reportBegin starts the run's Collector at its QueueStarted, before the
// Output's Effects: the first step's StartStep is in the same Output. A dry
// run has no report: nothing changes, and its in-process steps are all that
// runs.
func (d *Driver) reportBegin(o engine.Output) {
	if d.cfg.RunReport == nil {
		return
	}
	for _, ev := range o.Events {
		if q, ok := ev.(engine.QueueStarted); ok {
			d.rep = nil
			if !q.DryRun {
				d.rep = report.New(*d.cfg.RunReport, q.Action, q.Started)
			}
		}
	}
}

// reportCall runs f on the Collector, if there is one. A panic in it is
// reported and costs the run its report, never the loop.
func (d *Driver) reportCall(f func(c *report.Collector)) {
	c := d.rep
	if c == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			d.report(fmt.Errorf("driver: the run report panicked: %v", r))
			d.rep = nil
		}
	}()
	f(c)
}

// reportStarting tells the Collector a step is about to start.
func (d *Driver) reportStarting(e engine.StartStep) {
	d.reportCall(func(c *report.Collector) { c.StepStarting(d.live, e.Step) })
}

// reportStepDone tells the Collector how a step ended.
func (d *Driver) reportStepDone(id string, status steps.Status) {
	d.reportCall(func(c *report.Collector) { c.StepDone(d.live, id, status) })
}

// reportStepResult tells the Collector an in-process step's result.
func (d *Driver) reportStepResult(res steps.Result) {
	d.reportCall(func(c *report.Collector) { c.StepResult(res) })
}

// reportFinish makes the run's report and drops the Collector. A panic in
// it is reported and costs the report, never the run.
func (d *Driver) reportFinish(f engine.Finished) (rep *report.Report) {
	c := d.rep
	d.rep = nil
	if c == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			d.report(fmt.Errorf("driver: the run report panicked: %v", r))
			rep = nil
		}
	}()
	ctx, cancel := context.WithTimeout(d.live, report.DefaultFinishTimeout)
	defer cancel()
	r := c.Finish(ctx, report.FinishInfo{Elapsed: f.Elapsed, NotRun: f.NotRun})
	return &r
}

// writeLastRun carries out a WriteLastRun whose record gets rep's summary
// (when the report holds anything worth keeping).
func (d *Driver) writeLastRun(e engine.WriteLastRun, rep *report.Report) {
	if rep != nil && rep.HasData() {
		s := rep.Summary()
		e.Record.Summary = &logstore.RunSummary{
			Upgraded: s.Upgraded, AUR: s.AUR, FreedBytes: s.FreedBytes, Attention: s.Attention,
		}
	}
	d.effect(e)
}
