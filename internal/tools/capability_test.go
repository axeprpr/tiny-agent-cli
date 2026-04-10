package tools

import "testing"

func TestBundledCapabilityPacksIncludesCoreWorkflows(t *testing.T) {
	packs := BundledCapabilityPacks()
	if len(packs) < 4 {
		t.Fatalf("expected bundled capability packs, got %#v", packs)
	}
	for _, want := range []string{"ops", "release", "repo-research", "web-app"} {
		if _, ok := FindCapabilityPack(want); !ok {
			t.Fatalf("missing capability pack %q in %#v", want, packs)
		}
	}
}

func TestFindCapabilityPackNormalizesName(t *testing.T) {
	pack, ok := FindCapabilityPack(" Repo-Research ")
	if !ok {
		t.Fatalf("expected repo-research pack")
	}
	if pack.Name != "repo-research" {
		t.Fatalf("unexpected pack: %#v", pack)
	}
}
