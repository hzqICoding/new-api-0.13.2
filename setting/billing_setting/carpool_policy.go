package billing_setting

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const CarpoolBillingPolicyKey = "CarpoolBillingPolicy"

// Maps are owned by this snapshot and never exposed or mutated after adoption.
type carpoolBillingPolicy struct {
	issuedAt    time.Time
	groups      map[string]bool
	multipliers map[string]float64
}

func parseCarpoolBillingPolicy(raw string) (*carpoolBillingPolicy, error) {
	var fields map[string]json.RawMessage
	if common.UnmarshalJsonStr(raw, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("invalid JSON object")
	}
	var version int
	var issued string
	var groups []string
	var values map[string]json.RawMessage
	if common.Unmarshal(fields["version"], &version) != nil || version != 1 ||
		common.Unmarshal(fields["issued_at"], &issued) != nil || len(issued) <= 20 || issued[19] != '.' ||
		common.Unmarshal(fields["groups"], &groups) != nil || groups == nil ||
		common.Unmarshal(fields["model_multipliers"], &values) != nil || values == nil {
		return nil, fmt.Errorf("invalid policy fields")
	}
	stamp, err := time.Parse(time.RFC3339Nano, issued)
	if err != nil || stamp.IsZero() {
		return nil, fmt.Errorf("invalid issued_at")
	}
	p := &carpoolBillingPolicy{issuedAt: stamp, groups: map[string]bool{}, multipliers: map[string]float64{}}
	for _, group := range groups {
		if group == "" || p.groups[group] {
			return nil, fmt.Errorf("empty or duplicate group")
		}
		p.groups[group] = true
	}
	for name, value := range values {
		var ratio float64
		if name == "" || common.GetJsonType(value) != "number" || common.Unmarshal(value, &ratio) != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0.1 || ratio > 10 {
			return nil, fmt.Errorf("invalid model multiplier")
		}
		p.multipliers[name] = ratio
	}
	return p, nil
}

type carpoolBillingCache struct {
	mu       sync.Mutex
	lastRaw  string
	hasRaw   bool
	active   *carpoolBillingPolicy
	reported map[[32]byte]bool
	read     func() string
	log      func(string)
}

var carpoolBilling = carpoolBillingCache{
	read: func() string {
		common.OptionMapRWMutex.RLock()
		raw := common.OptionMap[CarpoolBillingPolicyKey]
		common.OptionMapRWMutex.RUnlock()
		return raw
	},
	log: common.SysError,
}

func (c *carpoolBillingCache) snapshot() *carpoolBillingPolicy {
	// Serialize source reads with publication so an older read cannot overtake a
	// newer update. OptionMap is held only while copying its immutable string.
	c.mu.Lock()
	raw := c.read()
	var diagnostic string
	if !c.hasRaw || raw != c.lastRaw {
		c.lastRaw, c.hasRaw = raw, true
		if raw != "" || c.active != nil {
			p, err := parseCarpoolBillingPolicy(raw)
			switch {
			case err != nil:
				diagnostic = "invalid policy; retaining last valid snapshot"
			case c.active == nil || p.issuedAt.After(c.active.issuedAt):
				c.active = p
			case p.issuedAt.Equal(c.active.issuedAt) && (!reflect.DeepEqual(p.groups, c.active.groups) || !reflect.DeepEqual(p.multipliers, c.active.multipliers)):
				diagnostic = "changed policy reused issued_at; retaining last valid snapshot"
				// Older versions and equivalent same-version publications are ignored.
			}
		}
	}
	if diagnostic != "" {
		if c.reported == nil {
			c.reported = map[[32]byte]bool{}
		}
		digest := sha256.Sum256([]byte(raw))
		if c.reported[digest] {
			diagnostic = ""
		} else {
			c.reported[digest] = true
		}
	}
	p := c.active
	c.mu.Unlock()
	if diagnostic != "" && c.log != nil {
		c.log("[carpool-billing] " + diagnostic)
	}
	return p
}

// CarpoolMultiplier uses exact, case-sensitive group and original-model keys.
// Missing/invalid startup configuration has no effect on the original billing.
func CarpoolMultiplier(group, model string) (float64, bool) {
	ratio, matched, _ := ResolveCarpoolBilling(group, model)
	return ratio, matched
}

// Group membership is returned from the same immutable policy as the ratio.
// It permits timestamp-only evidence for a known carpool model still at ×1.
func ResolveCarpoolBilling(group, model string) (ratio float64, matched, inGroup bool) {
	p := carpoolBilling.snapshot()
	if p == nil || !p.groups[group] {
		return 1, false, false
	}
	ratio, ok := p.multipliers[model]
	if !ok {
		return 1, false, true
	}
	return ratio, true, true
}
