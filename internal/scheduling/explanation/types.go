// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package explanation

import "time"

// ExplanationContext holds all data needed to render a complete explanation.
type ExplanationContext struct {
	Context       ContextData        `json:"context"`
	History       *HistoryData       `json:"history,omitempty"`
	Winner        *WinnerData        `json:"winner,omitempty"`
	Input         *InputData         `json:"input,omitempty"`
	CriticalSteps *CriticalStepsData `json:"criticalSteps,omitempty"`
	DeletedHosts  *DeletedHostsData  `json:"deletedHosts,omitempty"`
	StepImpacts   []StepImpact       `json:"stepImpacts,omitempty"`
	Chain         *ChainData         `json:"chain,omitempty"`
}

type ContextData struct {
	ResourceType   string `json:"resourceType"`
	DecisionNumber int    `json:"decisionNumber"`
	IsInitial      bool   `json:"isInitial"`
}

// HistoryData contains information about the previous decision in the chain.
type HistoryData struct {
	PreviousTarget string `json:"previousTarget"`
	CurrentTarget  string `json:"currentTarget"`
}

type WinnerData struct {
	HostName       string  `json:"hostName"`
	Score          float64 `json:"score"`
	Gap            float64 `json:"gap"`
	HostsEvaluated int     `json:"hostsEvaluated"`
	HasGap         bool    `json:"hasGap"`
}

// InputData contains information about input vs final winner comparison.
type InputData struct {
	InputWinner     string  `json:"inputWinner"`
	InputScore      float64 `json:"inputScore"`
	FinalWinner     string  `json:"finalWinner"`
	FinalScore      float64 `json:"finalScore"`
	FinalInputScore float64 `json:"finalInputScore"` // Final winner's input score
	InputConfirmed  bool    `json:"inputConfirmed"`
}

// CriticalStepsData contains information about which pipeline steps were critical.
type CriticalStepsData struct {
	Steps       []string `json:"steps"`
	TotalSteps  int      `json:"totalSteps"`
	IsInputOnly bool     `json:"isInputOnly"`
	RequiresAll bool     `json:"requiresAll"`
}

// DeletedHostsData contains information about hosts that were filtered out.
type DeletedHostsData struct {
	DeletedHosts []DeletedHostInfo `json:"deletedHosts"`
}

// DeletedHostInfo contains details about a single deleted host.
type DeletedHostInfo struct {
	Name          string   `json:"name"`
	Steps         []string `json:"steps"`
	IsInputWinner bool     `json:"isInputWinner"`
}

// ChainData contains information about the decision chain over time.
type ChainData struct {
	Segments []ChainSegment `json:"segments"`
	HasLoop  bool           `json:"hasLoop"`
}

// ChainSegment represents a period where the resource was on a specific host.
type ChainSegment struct {
	Host     string        `json:"host"`
	Duration time.Duration `json:"duration"`
	// number of decisions with this as the target host
	Decisions int `json:"decisions"`
}
