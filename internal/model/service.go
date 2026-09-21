// Package model holds shared domain types.
package model

import (
	"encoding/json"
)

// Strategy is a deploy strategy.
type Strategy string

// Supported deploy strategies.
const (
	StrategyUnknown   Strategy = "unknown"
	StrategyBlueGreen Strategy = "bluegreen"
	StrategyRecreate  Strategy = "recreate"
)

// Health is a service or color health state.
type Health string

// Supported health states.
const (
	HealthUnknown   Health = "unknown"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// Service is a managed service definition.
type Service struct {
	ID             string
	Name           string
	Strategy       Strategy
	ComposeProject string
	ComposeDir     string
	Config         json.RawMessage
}

// BlueGreenConfig is the strategy config for blue-green services.
type BlueGreenConfig struct {
	BlueService   string `json:"blue_service"`
	GreenService  string `json:"green_service"`
	BlueTarget    string `json:"blue_target"`
	GreenTarget   string `json:"green_target"`
	LegacyTarget  string `json:"legacy_target"`
	NginxConf     string `json:"nginx_conf"`
	Marker        string `json:"marker"`
	BlueURL       string `json:"blue_url"`
	GreenURL      string `json:"green_url"`
	PublicURL     string `json:"public_url"`
	CutoverScript string `json:"cutover_script"`
	RestoreScript string `json:"restore_script"`
	ComposeFile   string `json:"compose_file"`
	Project       string `json:"project"`
	EnvFile       string `json:"env_file"`
	Sudo          bool   `json:"sudo"`
	SettleSecs    int    `json:"settle_secs"`
	HealthTimeout int    `json:"health_timeout"`
	PublicTimeout int    `json:"public_timeout"`
}

// RecreateConfig is the strategy config for recreate services.
type RecreateConfig struct {
	Service        string `json:"service"`
	HealthURL      string `json:"health_url"`
	PublicURL      string `json:"public_url"`
	DeployScript   string `json:"deploy_script"`
	RollbackScript string `json:"rollback_script"`
}

// ServiceView is the API representation of a service with live state.
type ServiceView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Strategy   Strategy        `json:"strategy"`
	LiveColor  string          `json:"live_color"`
	Health     Health          `json:"health"`
	Colors     []ColorView     `json:"colors"`
	Containers []ContainerView `json:"containers"`
}

// ColorView is one blue/green color with its direct probe result.
type ColorView struct {
	Name      string `json:"name"`
	Live      bool   `json:"live"`
	Health    Health `json:"health"`
	Target    string `json:"target"`
	HealthURL string `json:"health_url"`
}

// ContainerView is one managed container.
type ContainerView struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Image  string   `json:"image"`
	SHA    string   `json:"sha"`
	State  string   `json:"state"`
	Status string   `json:"status"`
	Ports  []string `json:"ports"`
}
