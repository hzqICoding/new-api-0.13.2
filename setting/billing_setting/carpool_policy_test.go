package billing_setting

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func billingTestRaw(stamp, ratio string) string {
	return fmt.Sprintf(`{"version":1,"issued_at":%q,"groups":["carpool-a","cptm-B "],"model_multipliers":{"M":%s}}`, stamp, ratio)
}

func TestCarpoolBillingPolicyValidation(t *testing.T) {
	valid := billingTestRaw("2026-10-20T08:00:00.001Z", "2")
	for _, raw := range []string{valid, strings.Replace(valid, ":2}", ":0.1}", 1), strings.Replace(valid, ":2}", ":10}", 1)} {
		p, err := parseCarpoolBillingPolicy(raw)
		require.NoError(t, err)
		require.True(t, p.groups["cptm-B "])
		require.False(t, p.groups["cptm-B"])
	}
	invalid := []string{"", "null", "[]", "{}", strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `.001Z`, `Z`, 1), strings.Replace(valid, `"groups":["carpool-a","cptm-B "]`, `"groups":null`, 1), strings.Replace(valid, `"cptm-B "`, `"carpool-a"`, 1)}
	for _, ratio := range []string{"0", "-1", "0.099", "10.01", `"2"`, "null", "true", "1e999", "NaN", "{}", "[]"} {
		invalid = append(invalid, billingTestRaw("2026-10-20T08:00:00.001Z", ratio))
	}
	for _, raw := range invalid {
		_, err := parseCarpoolBillingPolicy(raw)
		require.Error(t, err, raw)
	}
}

func TestCarpoolBillingCacheRetainsValidPolicyAndDeduplicatesErrors(t *testing.T) {
	raw := ""
	logs := []string{}
	c := carpoolBillingCache{read: func() string { return raw }, log: func(message string) { logs = append(logs, message) }}
	require.Nil(t, c.snapshot())
	require.Empty(t, logs)
	raw = "invalid"
	require.Nil(t, c.snapshot())
	require.Len(t, logs, 1)
	raw = billingTestRaw("2026-10-20T08:00:01.001Z", "2")
	good := c.snapshot()
	require.Equal(t, 2.0, good.multipliers["M"])
	for _, rejected := range []string{
		billingTestRaw("2026-10-20T08:00:00.999Z", "3"),
		billingTestRaw("2026-10-20T08:00:01.001Z", "3"),
		billingTestRaw("2026-10-20T08:00:02.001Z", "-1"), "", "invalid",
	} {
		raw = rejected
		require.Same(t, good, c.snapshot())
		require.Same(t, good, c.snapshot())
	}
	require.Len(t, logs, 4) // Startup invalid, timestamp reuse, invalid value, empty.
	raw = billingTestRaw("2026-10-20T08:00:03.001Z", "1.5")
	next := c.snapshot()
	require.Equal(t, 1.5, next.multipliers["M"])
	require.Equal(t, 2.0, good.multipliers["M"], "published snapshots are immutable")
}

func TestCarpoolBillingCacheConcurrentReaders(t *testing.T) {
	c := carpoolBillingCache{read: func() string { return billingTestRaw("2026-10-20T08:00:00.001Z", "2") }}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p := c.snapshot()
				if p == nil || p.multipliers["M"] != 2 {
					t.Error("lost immutable policy")
				}
			}
		}()
	}
	wg.Wait()
}
