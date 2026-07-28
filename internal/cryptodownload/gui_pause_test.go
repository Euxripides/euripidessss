package cryptodownload

import "testing"

func TestPrepareResumeClearsResolvedAddressErrors(t *testing.T) {
	resolved := "csv token_transfers BSC: transient hydration error"
	job := &GUIJob{
		Status:  "paused",
		Message: "已暂停",
		Errors:  []string{resolved, "another address error"},
		entries: []GUIAddressChain{{Address: "0x57136ea9b2be6cd4ad74c3ca5b24172f87c9cb8d", Chain: "BSC"}},
		Addresses: []GUIAddressProgress{{
			Index: 0, Status: "paused", Errors: []string{resolved},
			Parts: []GUIAddressDownloadPart{{Key: "BSC|token_transfers", Status: "failed"}},
		}},
	}

	_, _, _, _, err := job.prepareResume()
	if err != nil {
		t.Fatalf("prepare resume failed: %v", err)
	}
	if len(job.Errors) != 1 || job.Errors[0] != "another address error" {
		t.Fatalf("unexpected retained job errors: %#v", job.Errors)
	}
	if len(job.Addresses[0].Errors) != 0 {
		t.Fatalf("resumed address errors were not cleared: %#v", job.Addresses[0].Errors)
	}
	if job.Addresses[0].Parts[0].Status != "pending" {
		t.Fatalf("expected failed part reset to pending, got %q", job.Addresses[0].Parts[0].Status)
	}
}
