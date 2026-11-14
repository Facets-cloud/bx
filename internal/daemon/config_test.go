package daemon

import "testing"

func TestAgentCannotLowerLogging(t *testing.T) {
	var c Config
	if c.Log.days() != 0 {
		t.Errorf("logs should be kept forever by default, got %d days", c.Log.days())
	}
	if err := c.Set("log.days", "365", true); err == nil {
		t.Error("an agent turned forever into 365 days")
	}
	if err := c.Set("log.days", "90", false); err != nil {
		t.Fatal(err)
	}
	for _, ok := range []struct{ key, value string }{
		{"log.days", "365"}, {"log.days", "0"}, {"log.max", "10GB"}, {"log.archive_max", "20GB"},
	} {
		if err := c.Set(ok.key, ok.value, true); err != nil {
			t.Errorf("an agent should be able to raise %s to %s: %v", ok.key, ok.value, err)
		}
	}
	for _, bad := range []struct{ key, value string }{
		{"log.days", "30"}, {"log.max", "1GB"}, {"log.archive_max", "1MB"}, {"log.values", "off"}, {"log.values", "full"},
	} {
		if err := c.Set(bad.key, bad.value, true); err == nil {
			t.Errorf("an agent lowered %s to %s", bad.key, bad.value)
		}
		if err := c.Set(bad.key, bad.value, false); err != nil {
			t.Errorf("the user should be able to set %s to %s: %v", bad.key, bad.value, err)
		}
	}
	if c.Log.values() != "full" || c.Log.days() != 30 || c.Log.maxBytes() != 1<<30 {
		t.Errorf("settings not applied: %+v", c.Log)
	}
	if err := c.Set("log.max", "lots", false); err == nil {
		t.Error("accepted a size that isn't one")
	}
}
