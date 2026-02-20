// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

/*import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"
)

type TemplateManager struct {
	templates *template.Template
}

func NewTemplateManager() (*TemplateManager, error) {
	tmpl := template.New("explanation").Funcs(template.FuncMap{
		"join":           strings.Join,
		"formatDuration": formatTemplateDuration,
		"formatFloat":    func(f float64) string { return fmt.Sprintf("%.2f", f) },
		"formatDelta":    func(f float64) string { return fmt.Sprintf("%+.2f", f) },
		"add":            func(a, b int) int { return a + b },
		"plural": func(n int, singular, plural string) string {
			if n == 1 {
				return singular
			}
			return plural
		},
	})

	tmpl, err := tmpl.Parse(mainTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse main template: %w", err)
	}

	templates := map[string]string{
		"context":  contextTemplate,
		"history":  historyTemplate,
		"winner":   winnerTemplate,
		"input":    inputTemplate,
		"critical": criticalTemplate,
		"deleted":  deletedTemplate,
		"impacts":  impactsTemplate,
		"chain":    chainTemplate,
	}

	for name, templateStr := range templates {
		tmpl, err = tmpl.Parse(fmt.Sprintf(`{{define "%s"}}%s{{end}}`, name, templateStr))
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s template: %w", name, err)
		}
	}

	return &TemplateManager{templates: tmpl}, nil
}

func (tm *TemplateManager) RenderExplanation(ctx ExplanationContext) (string, error) {
	var buf bytes.Buffer
	err := tm.templates.Execute(&buf, ctx)
	if err != nil {
		return "", fmt.Errorf("failed to render explanation: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func formatTemplateDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	// Truncate to seconds to remove sub-second precision
	d = d.Truncate(time.Second)

	// For durations >= 24 hours, convert to days format
	if d >= 24*time.Hour {
		days := int(d.Hours()) / 24
		remainder := d - time.Duration(days)*24*time.Hour
		if remainder == 0 {
			return fmt.Sprintf("%dd0h0m0s", days)
		}
		return fmt.Sprintf("%d%s", days, remainder.String())
	}

	// For shorter durations, use Go's built-in formatting
	return d.String()
}

const mainTemplate = `{{template "context" .Context}}
{{- if .History}} {{template "history" .History}}{{end}}
{{- if .Winner}} {{template "winner" .Winner}}{{end}}
{{- if .Input}} {{template "input" .Input}}{{end}}
{{- if .CriticalSteps}} {{template "critical" .CriticalSteps}}{{end}}
{{- if .DeletedHosts}} {{template "deleted" .DeletedHosts}}{{end}}
{{- if .StepImpacts}} {{template "impacts" .StepImpacts}}{{end}}
{{- if .Chain}} {{template "chain" .Chain}}{{end}}`

const contextTemplate = `{{if .IsInitial -}}
Initial placement of the {{.ResourceType}}.
{{- else -}}
Decision #{{.DecisionNumber}} for this {{.ResourceType}}.
{{- end}}`

const historyTemplate = `Previous target host was '{{.PreviousTarget}}', now it's '{{.CurrentTarget}}'.`

const winnerTemplate = `Selected: {{.HostName}} (score: {{formatFloat .Score}})
{{- if .HasGap}}, gap to 2nd: {{formatFloat .Gap}}{{end}}, {{.HostsEvaluated}} {{plural .HostsEvaluated "host" "hosts"}} evaluated.`

const inputTemplate = `{{if .InputConfirmed -}}
Input choice confirmed: {{.FinalWinner}} ({{formatFloat .InputScore}}→{{formatFloat .FinalScore}}).
{{- else -}}
Input favored {{.InputWinner}} ({{formatFloat .InputScore}}), final winner: {{.FinalWinner}} ({{formatFloat .FinalInputScore}}→{{formatFloat .FinalScore}}).
{{- end}}`

const criticalTemplate = `{{if .IsInputOnly -}}
Decision driven by input only (all {{.TotalSteps}} {{plural .TotalSteps "step is" "steps are"}} non-critical).
{{- else if .RequiresAll -}}
Decision requires all {{.TotalSteps}} pipeline {{plural .TotalSteps "step" "steps"}}.
{{- else if eq (len .Steps) 1 -}}
Decision driven by 1/{{.TotalSteps}} pipeline step: {{index .Steps 0}}.
{{- else -}}
Decision driven by {{len .Steps}}/{{.TotalSteps}} pipeline {{plural .TotalSteps "step" "steps"}}: {{join .Steps ", "}}.
{{- end}}`

const deletedTemplate = `{{len .DeletedHosts}} {{plural (len .DeletedHosts) "host" "hosts"}} filtered:
{{- range .DeletedHosts}}
 - {{.Name}}{{if .IsInputWinner}} (input choice){{end}} by {{join .Steps ", "}}
{{- end}}`

const impactsTemplate = ` Step impacts:
{{- range $i, $impact := .}}
• {{$impact.Step}}
{{- if $impact.PromotedToFirst}} {{formatDelta $impact.ScoreDelta}}→#1
{{- else if ne $impact.ScoreDelta 0.0}} {{formatDelta $impact.ScoreDelta}}
{{- else if gt $impact.CompetitorsRemoved 0}} +0.00 (removed {{$impact.CompetitorsRemoved}})
{{- else}} +0.00{{end}}
{{- end}}`

const chainTemplate = `{{if .HasLoop}}Chain (loop detected): {{else}}Chain: {{end}}
{{- range $i, $segment := .Segments}}{{if gt $i 0}} -> {{end}}{{$segment.Host}} ({{formatDuration $segment.Duration}}{{if gt $segment.Decisions 1}}; {{$segment.Decisions}} decisions{{end}}){{end}}.`
*/
