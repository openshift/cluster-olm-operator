# Vendor Patches

This document tracks patches applied to vendored dependencies.

## openshift/library-go - HasSyncedChecker compatibility patch

**File**: `vendor/github.com/openshift/library-go/pkg/operator/v1helpers/test_helpers.go`

**Reason**: Kubernetes v0.36.0 added the `HasSyncedChecker()` method to the `cache.SharedIndexInformer` interface. The upstream openshift/library-go hasn't been updated to implement this method yet.

**Patch Details**:
1. Added `fakeDoneChecker` type implementing the `cache.DoneChecker` interface
2. Added `HasSyncedChecker()` method to `fakeSharedIndexInformer`

**When to remove**: This patch can be removed once openshift/library-go is updated to be compatible with k8s.io/client-go v0.36.x.

**Re-application**: If you run `go mod vendor`, this patch will need to be re-applied. The patch adds:

```go
type fakeDoneChecker struct{}

func (f *fakeDoneChecker) Name() string {
	return "fake-done-checker"
}

func (f *fakeDoneChecker) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
```

And adds this method to `fakeSharedIndexInformer`:

```go
func (fakeSharedIndexInformer) HasSyncedChecker() cache.DoneChecker {
	return &fakeDoneChecker{}
}
```
