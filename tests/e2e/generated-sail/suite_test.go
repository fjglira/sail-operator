//go:build e2e

package e2e_generated

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2eGenerated(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2eGenerated Suite")
}

var _ = BeforeSuite(func() {
	// User's custom setup
	fmt.Println("setting up cluster...")
})

var _ = AfterSuite(func() {
	// User's custom teardown
	fmt.Println("tearing down...")
})
