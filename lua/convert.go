package lua

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/iceisfun/golua/v2/vm"
)

// maxDepth bounds nesting when converting tables, which also stops cycles.
const maxDepth = 64

// plain turns a Go payload into JSON-shaped values: map[string]any, []any,
// string, float64, bool, and nil. Field names follow json tags.
func plain(payload any) (any, error) {
	switch payload.(type) {
	case nil, bool, string, float64, map[string]any, []any:
		return payload, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(data, &out)
}

// toLua converts a JSON-shaped value into a Lua value.
func toLua(x any) vm.Value {
	switch x := x.(type) {
	case bool:
		return vm.NewBool(x)
	case float64:
		if x == float64(int64(x)) {
			return vm.NewInt(int64(x))
		}
		return vm.NewFloat(x)
	case int64:
		return vm.NewInt(x)
	case string:
		return vm.NewString(x)
	case []any:
		t := vm.NewTableWithSize(len(x), 0)
		for i, item := range x {
			_ = t.Set(vm.NewInt(int64(i+1)), toLua(item))
		}
		return vm.NewTable(t)
	case []string:
		t := vm.NewTableWithSize(len(x), 0)
		for i, item := range x {
			_ = t.Set(vm.NewInt(int64(i+1)), vm.NewString(item))
		}
		return vm.NewTable(t)
	case map[string]any:
		t := vm.NewTableWithSize(0, len(x))
		for k, v := range x {
			t.SetString(k, toLua(v))
		}
		return vm.NewTable(t)
	}
	return vm.Nil
}

// fromLua converts a Lua value into a JSON-shaped Go value. A table whose keys
// are exactly 1..n becomes a list; any other table becomes a map with string keys.
func fromLua(v vm.Value) (any, error) { return fromLuaDepth(v, 0) }

func fromLuaDepth(v vm.Value, depth int) (any, error) {
	switch {
	case v.IsNil():
		return nil, nil
	case v.IsBool():
		return v.AsBool(), nil
	case v.IsInt():
		return v.AsInt(), nil
	case v.IsFloat():
		return v.AsFloat(), nil
	case v.IsString():
		return v.AsString(), nil
	case v.IsTable():
		if depth >= maxDepth {
			return nil, fmt.Errorf("table nested deeper than %d levels", maxDepth)
		}
		return tableFromLua(v.AsTable(), depth+1)
	}
	return nil, fmt.Errorf("cannot convert a Lua %s", v.Type())
}

func tableFromLua(t vm.LuaTable, depth int) (any, error) {
	entries := map[string]any{}
	var ints []int64
	key := vm.Nil
	for {
		next, value, err := t.Next(key)
		if err != nil {
			return nil, err
		}
		if next.IsNil() {
			break
		}
		key = next
		converted, err := fromLuaDepth(value, depth)
		if err != nil {
			return nil, err
		}
		switch {
		case next.IsString():
			entries[next.AsString()] = converted
		case next.IsInt():
			ints = append(ints, next.AsInt())
			entries[strconv.FormatInt(next.AsInt(), 10)] = converted
		default:
			return nil, fmt.Errorf("table keys must be strings or integers, not %s", next.Type())
		}
	}
	if len(ints) > 0 && len(ints) == len(entries) {
		slices.Sort(ints)
		if ints[0] == 1 && ints[len(ints)-1] == int64(len(ints)) {
			list := make([]any, len(ints))
			for i := range list {
				list[i] = entries[strconv.Itoa(i+1)]
			}
			return list, nil
		}
	}
	return entries, nil
}
