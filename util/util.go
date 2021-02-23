package util

import (
	"encoding/json"
	"fmt"
	"math/big"
)

func ToArray(m *map[string]interface{}, key string) []interface{} {
	if m != nil {
		data := (*m)[key]
		if data != nil {
			list, ok := data.([]interface{})
			if ok {
				return list
			}
		}
	}
	return nil
}

func ToUint64(m *map[string]interface{}, key string) uint64 {
	s := ToString(m, key)
	if len(s) > 0 {
		n := new(big.Int)
		_, ok := n.SetString(s, 10)
		if ok {
			return n.Uint64()
		}
	}
	return 0
}

func ToInt64(m *map[string]interface{}, key string) int64 {
	s := ToString(m, key)
	if len(s) > 0 {
		n := new(big.Int)
		_, ok := n.SetString(s, 10)
		if ok {
			return n.Int64()
		}
	}
	return 0
}

func ToString(m *map[string]interface{}, key string) string {
	item, ok := (*m)[key]
	if ok {
		s, ok := item.(string)
		if ok {
			return s
		}
	}
	return ""
}

func PrintJSON(name string, o interface{}) {
	jsonBytes, err := json.MarshalIndent(o, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println(name, string(jsonBytes))
}
