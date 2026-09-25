// SPDX-License-Identifier: Apache-2.0

package auth

import "encoding/json"

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
