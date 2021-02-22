package util

import (
	"encoding/json"
	"fmt"
)

func PrintJSON(name string, o interface{}) {
	jsonBytes, err := json.MarshalIndent(o, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Println(name, string(jsonBytes))
}
