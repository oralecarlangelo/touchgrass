package service

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package on goroutine leaks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// Seeded service ids used across service tests.
const (
	testServiceAPI     = "tn-api"
	testServiceFE      = "tn-fe"
	testServiceAdminFE = "admin-fe"
)

// Shared fake values used across service tests.
const (
	testAdminActor         = "admin"
	testRecreateService    = "fe"
	testRecreateHealthURL  = "http://127.0.0.1:3200/health"
	testUnknownServiceID   = "no-such-service"
	testUnknownServiceName = "unknown service"
	testBogusValue         = "bogus"
	testBlueHealthURL      = "http://127.0.0.1:4101/health"
	testGreenHealthURL     = "http://127.0.0.1:4102/health"
	testAdminHealthURL     = "http://127.0.0.1:3002/health"
	testComposeProject     = "ticketnation"
	testRunningState       = "running"
	testActor              = "tester"
	testBlueService        = "api-blue"
	testBlueContainerID    = "blue-id"
	testBlueContainerName  = "ticketnation-api-blue-1"
	testOtherProject       = "other"
	testGreenService       = "api-green"
	testAdminService       = "app"
	testTargetFlag         = "--target"
	testTrueBinary         = "/bin/true"
	testServiceFlag        = "--service"
	testComposeDir         = "/opt/test"
)
