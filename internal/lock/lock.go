// Package lock provides advisory host locks with a fixed acquisition
// order. Lock names encode the order class; helpers attach the
// acquisition site to timeout errors.
//
// Fixed order (never acquire an earlier class while holding a later
// one): store-intent → store-global → artifact/candidate → project.
// The intent gate is internal to the locker: every acquisition of a
// name first passes through that name's intent lock, so exclusive
// waiters cannot be systematically starved by streams of shared
// holders (see AcquireShared).
package lock

import "context"

// Locker acquires named advisory locks scoped to one host layout.
type Locker interface {
	// Acquire blocks until the named exclusive lock is held or ctx is
	// done. The returned Lock must be released by the same process.
	Acquire(ctx context.Context, name string) (Lock, error)
	// AcquireShared blocks until the named lock is held shared or ctx
	// is done. Shared holders coexist with each other and exclude
	// exclusive holders.
	AcquireShared(ctx context.Context, name string) (Lock, error)
}

// Lock is one held advisory lock.
type Lock interface {
	Release() error
}

// Well-known name constructors keep the lock order visible at call
// sites and out of string literals.

func StoreGlobal() string { return "00-store" }

func Object(kind, digestHex string) string { return "10-object-" + kind + "-" + digestHex }

func Project(id string) string { return "20-project-" + id }
