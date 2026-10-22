package mcpserver

import "testing"

func TestImageAndHostTools(t *testing.T) {
	cs, _ := connect(t) // empty XDG_CACHE_HOME: no images
	if res, out := call(t, cs, "image_list", nil); res.IsError || len(out["images"].([]any)) != 0 {
		t.Errorf("image_list: %s", text(res))
	}
	if res, out := call(t, cs, "image_prune", map[string]any{"dryRun": true}); res.IsError || len(out["deleted"].([]any)) != 0 {
		t.Errorf("image_prune: %s", text(res))
	}
	res, out := call(t, cs, "host_check", nil)
	checks, _ := out["checks"].([]any)
	if res.IsError || len(checks) < 5 {
		t.Errorf("host_check: %s", text(res))
	}
}
