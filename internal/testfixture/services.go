// Package testfixture holds service definitions shared by the store,
// service and http test suites. It is imported by tests only.
package testfixture

import (
	"encoding/json"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// DogfoodServices returns the three services of the first dogfood
// deployment: one blue-green API and two recreate front ends. They used
// to be inserted by migration 0002 into every install; a fresh database
// now starts empty and the suites that exercise a populated inventory
// seed these rows themselves. Each call returns fresh values.
func DogfoodServices() []model.Service {
	return []model.Service{
		{
			ID: "tn-api", Name: "tn-api", Strategy: model.StrategyBlueGreen,
			ComposeProject: "ticketnation", ComposeDir: "/opt/ticketnation",
			Config: json.RawMessage(`{"blue_service":"api-blue","green_service":"api-green","blue_target":"127.0.0.1:4101","green_target":"127.0.0.1:4102","legacy_target":"127.0.0.1:4000","nginx_conf":"/etc/nginx/sites-available/ticketnation","marker":"# BLUEGREEN-ACTIVE","blue_url":"http://127.0.0.1:4101/health","green_url":"http://127.0.0.1:4102/health","public_url":"https://api.ticketnation.ph/health"}`),
		},
		{
			ID: "tn-fe", Name: "tn-fe", Strategy: model.StrategyRecreate,
			ComposeProject: "ticketnation-fe", ComposeDir: "/opt/ticketnation-fe",
			Config: json.RawMessage(`{"service":"fe","health_url":"http://127.0.0.1:3000","public_url":"https://ticketnation.ph"}`),
		},
		{
			ID: "admin-fe", Name: "admin-fe", Strategy: model.StrategyRecreate,
			ComposeProject: "ticketnation-admin", ComposeDir: "/opt/ticketnation-admin",
			Config: json.RawMessage(`{"service":"app","health_url":"http://127.0.0.1:3002/health","public_url":"https://admin.ticketnation.ph/health"}`),
		},
	}
}

// DefaultRule returns the alert rule the old seed gave each of those
// services (migration 0003 added one per service present at the time):
// memory above 85% for five minutes.
func DefaultRule(serviceID string) model.RuleCreate {
	return model.RuleCreate{
		ServiceID: serviceID, Metric: model.MetricMem, Threshold: 85, DurationSecs: 300,
	}
}
