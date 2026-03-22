package interpreter

import (
	"fmt"
	"sort"
)

// objectToGoValue converts a Codong Object to a native Go value.
func objectToGoValue(obj Object) interface{} {
	switch o := obj.(type) {
	case *NumberObject:
		if o.Value == float64(int64(o.Value)) {
			return int64(o.Value)
		}
		return o.Value
	case *StringObject:
		return o.Value
	case *BoolObject:
		return o.Value
	case *NullObject:
		return nil
	case *ListObject:
		result := make([]interface{}, len(o.Elements))
		for i, el := range o.Elements {
			result[i] = objectToGoValue(el)
		}
		return result
	case *MapObject:
		result := make(map[string]interface{})
		for k, v := range o.Entries {
			result[k] = objectToGoValue(v)
		}
		return result
	default:
		return fmt.Sprintf("%v", obj.Inspect())
	}
}

// goValueToObject converts a native Go value to a Codong Object.
func goValueToObject(v interface{}) Object {
	if v == nil {
		return NULL_OBJ
	}
	switch val := v.(type) {
	case bool:
		return nativeBoolToObject(val)
	case int:
		return &NumberObject{Value: float64(val)}
	case int64:
		return &NumberObject{Value: float64(val)}
	case float64:
		return &NumberObject{Value: val}
	case string:
		return &StringObject{Value: val}
	case []interface{}:
		elements := make([]Object, len(val))
		for i, el := range val {
			elements[i] = goValueToObject(el)
		}
		return &ListObject{Elements: elements}
	case map[string]interface{}:
		entries := make(map[string]Object)
		order := make([]string, 0, len(val))
		// Sort keys for deterministic order
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			entries[k] = goValueToObject(val[k])
			order = append(order, k)
		}
		return &MapObject{Entries: entries, Order: order}
	case []byte:
		return &StringObject{Value: string(val)}
	default:
		return &StringObject{Value: fmt.Sprintf("%v", val)}
	}
}

// mapObjectToStringMap extracts string values from a MapObject for simple key-value use.
func mapObjectToStringMap(m *MapObject) map[string]string {
	result := make(map[string]string)
	for k, v := range m.Entries {
		if s, ok := v.(*StringObject); ok {
			result[k] = s.Value
		} else {
			result[k] = v.Inspect()
		}
	}
	return result
}
