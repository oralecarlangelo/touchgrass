package testfixture

import (
	"encoding/json"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Generic service ids. They name no real deployment: suites that prove
// reusable behaviour (who may see which service) use them so the proof
// does not lean on the dogfood fixture.
const (
	// GenericGranted is the service the scoped fixtures are granted.
	GenericGranted = "billing-worker"
	// GenericOther is a service they are not granted.
	GenericOther = "docs-site"
)

// GenericServices returns two plain recreate services with no tie to
// any deployment. Each call returns fresh values.
func GenericServices() []model.Service {
	return []model.Service{
		{
			ID: GenericGranted, Name: "Billing worker", Strategy: model.StrategyRecreate,
			ComposeProject: "billing", ComposeDir: "/srv/billing",
			Config: json.RawMessage(`{"service":"worker","health_url":"http://127.0.0.1:7101/health"}`),
		},
		{
			ID: GenericOther, Name: "Docs site", Strategy: model.StrategyRecreate,
			ComposeProject: "docs", ComposeDir: "/srv/docs",
			Config: json.RawMessage(`{"service":"web","health_url":"http://127.0.0.1:7102/health"}`),
		},
	}
}
