//go:build e2e

package e2e_generated

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2eGenerated(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2eGenerated Suite")
}
