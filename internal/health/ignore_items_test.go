package health

import (
	"context"
	"reflect"
	"testing"
)

func TestItemsGoneAndDrives(t *testing.T) {
	ctx := context.Background()
	c := checkAUR(ctx, Deps{Foreign: func() ([]Foreign, error) { return []Foreign{{Name: "a"}, {Name: "b"}}, nil },
		AURSeen: func() (map[string]bool, bool) { return map[string]bool{"a": true, "b": true}, true },
		AURInfo: func(context.Context, []string) ([]AURPkg, error) { return []AURPkg{{Name: "a"}}, nil }})
	if c.State != Look || !reflect.DeepEqual(c.Items, []Item{{"aur-gone:b", "b"}}) {
		t.Errorf("gone: %+v", c)
	}
	body := drivesJSON(map[string]map[string]map[string]any{"/org/freedesktop/UDisks2/drives/WD_Blue": {
		ifaceDrive: P{"Model": "WD Blue"}, ifaceAta: {"SmartSupported": true, "SmartUpdated": 5, "SmartFailing": true}}})
	d := checkDiskHealth(ctx, Deps{Query: func(context.Context, string, ...string) ([]byte, error) { return []byte(body), nil }})
	if d.State != Look || !reflect.DeepEqual(d.Items, []Item{{"drive:WD_Blue", "WD Blue"}}) {
		t.Errorf("drive: %+v", d)
	}
}
