package allowlist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func reload(emails string) {
	set = parse(emails)
}

func TestAllowed_ExactMatch(t *testing.T) {
	reload("owner@mocbydylan.com,staff@mocbydylan.com")
	assert.True(t, Allowed("owner@mocbydylan.com"))
	assert.True(t, Allowed("staff@mocbydylan.com"))
}

func TestAllowed_NotInList(t *testing.T) {
	reload("owner@mocbydylan.com")
	assert.False(t, Allowed("intruder@evil.com"))
}

func TestAllowed_NormalisesInput(t *testing.T) {
	reload("owner@mocbydylan.com")
	assert.True(t, Allowed("  OWNER@MOCBYDYLAN.COM  "))
	assert.True(t, Allowed("Owner@MocByDylan.com"))
}

func TestAllowed_NormalisedInList(t *testing.T) {
	// Allow-list entries that have whitespace or mixed case are normalised on parse.
	reload("  OWNER@MOCBYDYLAN.COM , Staff@MocByDylan.com ")
	assert.True(t, Allowed("owner@mocbydylan.com"))
	assert.True(t, Allowed("staff@mocbydylan.com"))
}

func TestAllowed_EmptyList(t *testing.T) {
	reload("")
	assert.False(t, Allowed("owner@mocbydylan.com"))
}

func TestAllowed_EmptyEmail(t *testing.T) {
	reload("owner@mocbydylan.com")
	assert.False(t, Allowed(""))
	assert.False(t, Allowed("   "))
}

func TestAllowed_MultipleEmails(t *testing.T) {
	reload("owner@mocbydylan.com,staff@mocbydylan.com,kho@mocbydylan.com")
	assert.True(t, Allowed("kho@mocbydylan.com"))
	assert.False(t, Allowed("other@mocbydylan.com"))
}

func TestReload(t *testing.T) {
	reload("owner@mocbydylan.com")
	assert.True(t, Allowed("owner@mocbydylan.com"))

	// Simulate env change then Reload.
	t.Setenv("INVENTORY_ALLOWED_EMAILS", "newstaff@mocbydylan.com")
	Reload()
	assert.False(t, Allowed("owner@mocbydylan.com"))
	assert.True(t, Allowed("newstaff@mocbydylan.com"))
}

func TestParse_IgnoresBlankEntries(t *testing.T) {
	m := parse(",,,owner@mocbydylan.com,,,")
	assert.Len(t, m, 1)
	_, ok := m["owner@mocbydylan.com"]
	assert.True(t, ok)
}
