//go:build testhooks

package match

import "github.com/jafo/isnipes/internal/sim"

// simForTest exposes the internal sim pointer so cross-pkg testhooks
// (e.g. SetServerTickForTest) can mutate it. Test-only.
func (m *Match) simForTest() *sim.Sim { return m.sim }
