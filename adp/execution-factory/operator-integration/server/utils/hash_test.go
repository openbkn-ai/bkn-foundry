package utils

import "testing"

type TestHash struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

func TestObjectUUIDHash(t *testing.T) {
	a := &TestHash{
		Name: "test",
		Desc: "test",
	}
	hash, err := ObjectUUIDHash(a)
	if err != nil {
		t.Fatal(err)
	}
	const want = "613a1d33-ffce-f91d-8a7c-475325b6689d"
	if hash != want {
		t.Fatalf("ObjectUUIDHash = %q, want %q", hash, want)
	}
}
