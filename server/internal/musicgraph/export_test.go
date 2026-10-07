// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import "time"

// SetBackOff shortens how long a throttled source is left alone, for tests.
func SetBackOff(d time.Duration) (restore func()) {
	old := backOffFor
	backOffFor = d
	return func() { backOffFor = old }
}
