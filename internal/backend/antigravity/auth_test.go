package antigravity

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// 2026-09-30：BADAPT-005／007，過期與有效登入均須驗證，不能保留 refresh。
func TestAccessOnlySnapshot(t *testing.T) {
	data := []byte(`{"token":{"access_token":"access-probe","refresh_token":"refresh-probe","expiry":"` + time.Now().Add(time.Hour).Format(time.RFC3339Nano) + `","token_type":"Bearer"},"auth_method":"oauth","id_token":"id-probe"}`)
	auth, err := decodeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(auth.Payload, []byte("refresh-probe")) || bytes.Contains(auth.Payload, []byte("refresh_token")) {
		t.Fatal("refresh credential survived snapshot")
	}
	var d map[string]interface{}
	if json.Unmarshal(auth.Payload, &d) != nil {
		t.Fatal("invalid snapshot")
	}
	expired := []byte(`{"token":{"access_token":"access-probe","expiry":"2026-09-18T21:55:41+08:00"}}`)
	if _, err := decodeSnapshot(expired); err == nil {
		t.Fatal("expired credential accepted")
	}
}
