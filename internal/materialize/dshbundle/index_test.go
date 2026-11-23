package dshbundle

import (
	"strings"
	"testing"
)

func TestHostBundleDoesNotDeclarePlainObjectConfig(t *testing.T) {
	body, err := FS.ReadFile("index.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "export const Config = {") {
		t.Fatal("DSH requires Config to be a native Schemastery schema; a plain object prevents Host activation")
	}
}
