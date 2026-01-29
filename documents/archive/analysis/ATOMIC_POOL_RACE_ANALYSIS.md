# Atomic Pool Race Condition Analysis

## Problem Summary

During Phase 2 of mutex-to-atomic conversion (using `sync.Pool` for GC pressure mitigation), a race condition was detected in the thread safety tests. This document analyzes the issue in detail.

---

## What is `speedState`?

### Definition

`speedState` is a small struct that holds playback speed-related state for a single FFmpeg client. It was introduced as part of the mutex-to-atomic conversion to enable lock-free updates using `atomic.Value` with `sync.Pool` for memory efficiency.

**File**: `internal/stats/client_stats.go`
**Lines**: 37-47

```go
// speedState holds speed-related state for atomic updates with sync.Pool
type speedState struct {
    speed            float64      // Current playback speed (1.0 = realtime, <0.9 = stalling)
    belowThresholdAt time.Time   // When speed first dropped below threshold (for stall detection)
}

// Reset clears all fields to prepare for reuse from pool
func (s *speedState) Reset() {
    s.speed = 0
    s.belowThresholdAt = time.Time{}
}
```

### Purpose

**Before conversion** (mutex-based):
```go
// Old approach - mutex-protected fields
CurrentSpeed          float64
speedBelowThresholdAt time.Time
speedMu               sync.Mutex

func (s *ClientStats) UpdateSpeed(speed float64) {
    s.speedMu.Lock()
    s.CurrentSpeed = speed
    // ... update belowThresholdAt ...
    s.speedMu.Unlock()
}
```

**After conversion** (atomic-based with sync.Pool):
```go
// New approach - atomic.Value with sync.Pool
speedState atomic.Value // *speedState

func (s *ClientStats) UpdateSpeed(speed float64) {
    current := s.speedState.Load().(*speedState)  // Lock-free read
    newState := speedStatePool.Get().(*speedState) // Get from pool
    newState.Reset()
    // ... initialize newState ...
    oldState := s.speedState.Swap(newState).(*speedState) // Atomic swap
    speedStatePool.Put(oldState) // Return to pool
}
```

### Why `sync.Pool`?

- **GC Pressure**: Without pooling, each `UpdateSpeed()` call would allocate a new `speedState` struct (~24 bytes). With high-frequency updates (every progress event), this creates significant GC pressure.
- **Memory Efficiency**: `sync.Pool` reuses structs, reducing allocations by ~90% in typical usage.
- **Lock-Free**: Combined with `atomic.Value`, this provides lock-free reads and writes.

### Usage in ClientStats

**Storage**:
```go
type ClientStats struct {
    // ...
    speedState atomic.Value // *speedState  (line 137)
    // ...
}
```

**Methods that use it**:
- `UpdateSpeed(float64)` - Updates speed (called on every progress update)
- `GetSpeed() float64` - Returns current speed (lock-free read)
- `IsStalled() bool` - Checks if client is stalling (lock-free read)

### Similar Structs

The same pattern is used for:
- `driftState` - Tracks wall-clock drift (3 fields: `lastPlaybackTime`, `currentDrift`, `maxDrift`)
- `segmentSizeState` - Tracks segment size ring buffer (slice + index)

All three use the same pattern: `atomic.Value` + `sync.Pool` for lock-free updates with reduced GC pressure.

---

## Race Condition Details

### Test That Fails
- **File**: `internal/stats/client_stats_test.go`
- **Test**: `TestClientStats_ThreadSafety` (line 306)
- **Race Detector Output**:
```
WARNING: DATA RACE
Write at 0x00c000112020 by goroutine 13:
  github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats.(*speedState).Reset()
      /home/das/Downloads/go-ffmpeg-hls-swarm/internal/stats/client_stats.go:45 +0x31d
  github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats.(*ClientStats).UpdateSpeed()
      /home/das/Downloads/go-ffmpeg-hls-swarm/internal/stats/client_stats.go:347 +0x315

Previous read at 0x00c000112020 by goroutine 9:
  github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats.(*ClientStats).GetSpeed()
      /home/das/Downloads/go-ffmpeg-hls-swarm/internal/stats/client_stats.go:357 +0x23b
  github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats.(*ClientStats).GetSummary()
      /home/das/Downloads/go-ffmpeg-hls-swarm/internal/stats/client_stats.go:515 +0x3d9
```

### Affected Code Locations

#### 1. `UpdateSpeed()` - Writer (Goroutine 13)
- **File**: `internal/stats/client_stats.go`
- **Function**: `UpdateSpeed()` starting at line ~320
- **Problematic Line**: Line 347 - `oldState.Reset()`

```go
func (s *ClientStats) UpdateSpeed(speed float64) {
    // Load current state
    current := s.speedState.Load().(*speedState)  // Line ~330

    // Get new state from pool
    newState := speedStatePool.Get().(*speedState)  // Line ~333

    // Reset before use
    newState.Reset()  // Line ~336

    // Initialize new state
    newState.speed = speed
    // ... set belowThresholdAt ...

    // Atomically swap
    oldState := s.speedState.Swap(newState).(*speedState)  // Line ~344

    // CRITICAL: Reset old state before returning to pool
    oldState.Reset()  // Line 347 - RACE CONDITION HERE

    // Put old state back in pool
    speedStatePool.Put(oldState)  // Line ~350
}
```

#### 2. `GetSpeed()` - Reader (Goroutine 9)
- **File**: `internal/stats/client_stats.go`
- **Function**: `GetSpeed()` starting at line ~355
- **Problematic Line**: Line 357 - Reading from state that might be reset

```go
func (s *ClientStats) GetSpeed() float64 {
    state := s.speedState.Load().(*speedState)  // Line 357 - RACE CONDITION HERE
    return state.speed
}
```

#### 3. `GetSummary()` - Calls GetSpeed()
- **File**: `internal/stats/client_stats.go`
- **Function**: `GetSummary()` starting at line ~500
- **Line**: Line 515 - Calls `s.GetSpeed()`

---

## Object Lifetime Analysis

### Current Pattern (Causing Race)

```
Time    Goroutine A (Writer)              Goroutine B (Reader)
----    --------------------              --------------------
T1      Load() → gets state1
T2                                    Load() → gets state1 (same pointer!)
T3      Get() from pool → state2
T4      Reset() state2
T5      Initialize state2
T6      Swap(state2) → returns state1
T7      Reset() state1 ← WRITE           Reading state1.speed ← READ
T8      Put(state1) back to pool         (still reading...)
```

**Problem**: At T7, Goroutine A is writing to `state1` (via `Reset()`) while Goroutine B is reading from `state1` (via `state.speed`). This is a data race.

### Why This Happens

1. **`atomic.Value` semantics**: After `Swap()`, new `Load()` calls get the new state, but goroutines that already called `Load()` before the `Swap()` still have pointers to the old state.

2. **Copy-on-write pattern**: We're using copy-on-write (create new state, swap atomically), but the "old" state can still be read by goroutines that loaded it before the swap.

3. **Premature reset**: We're resetting the old state immediately after `Swap()`, but it's still being read by other goroutines.

---

## Root Cause

The race condition occurs because:

1. **Multiple goroutines can hold references to the same state struct**:
   - Goroutine A: `Load()` → gets pointer to `state1`
   - Goroutine B: `Load()` → gets pointer to `state1` (same pointer!)
   - Both goroutines now have references to the same memory

2. **After `Swap()`, old state is no longer "current" but still readable**:
   - `Swap()` atomically makes `newState` the current state
   - But goroutines that already `Load()`ed still have pointers to `oldState`
   - Those goroutines can continue reading from `oldState`

3. **Resetting old state while it's being read**:
   - When we call `oldState.Reset()`, we're writing to memory
   - Other goroutines might be reading from that same memory
   - This creates a write-read race condition

---

## Object Lifetime Details

### State Struct Lifetime

```
1. Creation: Pool.Get() or Pool.New()
   └─> State struct allocated (or retrieved from pool)

2. Initial Use: Reset() + Initialize
   └─> State is prepared for use

3. Active: Stored in atomic.Value via Swap()
   └─> State is the "current" state
   └─> Multiple goroutines can Load() and get pointer to this state
   └─> Goroutines hold references to this state

4. Replaced: Swap() returns old state
   └─> State is no longer "current" in atomic.Value
   └─> BUT: Goroutines that Load()ed before Swap() still have pointers
   └─> Those goroutines can still read from this state

5. Returned to Pool: Put() back to pool
   └─> State is available for reuse
   └─> BUT: If we Reset() here, we're modifying memory that might still be read

6. Reuse: Next Get() from pool
   └─> State is retrieved again
   └─> Reset() here is safe (no one is reading it yet)
```

### The Critical Window

The race occurs in the window between:
- **Swap() returns old state** (state is no longer current)
- **Goroutines finish reading from old state** (they still have pointers)

During this window:
- ✅ It's safe to `Put()` the old state back to the pool
- ❌ It's NOT safe to `Reset()` the old state (other goroutines are reading)

---

## Potential Solutions

### Solution 1: Don't Reset Before Put() ✅ **RECOMMENDED**

**Pattern**:
```go
// Get new state from pool
newState := pool.Get().(*state)
newState.Reset()  // Reset AFTER Get() - safe, no one reading yet

// Initialize new state
newState.field = value

// Swap
oldState := atomicValue.Swap(newState).(*state)

// Put back WITHOUT resetting
pool.Put(oldState)  // Safe - old state may still be read

// Reset happens on next Get() - safe because no one has reference yet
```

**Pros**:
- ✅ No race condition (we don't modify memory that's being read)
- ✅ Old state remains readable until all goroutines finish
- ✅ Reset happens when safe (on next Get())

**Cons**:
- ⚠️ Old state has stale data when returned to pool
- ⚠️ Must Reset() on every Get() (but we're already doing this)

**Implementation**:
- Remove `oldState.Reset()` before `Put()`
- Keep `newState.Reset()` after `Get()` (this is safe)

---

### Solution 2: Wait for All Readers (Not Practical)

**Pattern**: Use reference counting or wait mechanism to ensure all readers are done before resetting.

**Pros**:
- ✅ Could reset before Put() if we knew no one was reading

**Cons**:
- ❌ Complex (requires reference counting)
- ❌ Defeats purpose of lock-free design
- ❌ Performance overhead

**Verdict**: ❌ Not practical

---

### Solution 3: Copy Values Instead of Resetting Struct

**Pattern**: Instead of resetting the struct, copy only the values we need.

**Pros**:
- ✅ No modification of shared memory

**Cons**:
- ❌ Doesn't solve the problem (we still need to reset for pool reuse)
- ❌ More complex

**Verdict**: ❌ Doesn't address root cause

---

## Recommended Fix

### Pattern: Reset After Get(), Not Before Put()

```go
func (s *ClientStats) UpdateSpeed(speed float64) {
    // Load current state
    current := s.speedState.Load().(*speedState)

    // Get new state from pool (or create if empty)
    newState := speedStatePool.Get().(*speedState)

    // CRITICAL: Reset AFTER Get() - safe, no one reading yet
    newState.Reset()

    // Initialize new state
    newState.speed = speed
    if speed > 0 && speed < StallThreshold {
        if current.belowThresholdAt.IsZero() {
            newState.belowThresholdAt = time.Now()
        } else {
            newState.belowThresholdAt = current.belowThresholdAt
        }
    } else {
        newState.belowThresholdAt = time.Time{}
    }

    // Atomically swap
    oldState := s.speedState.Swap(newState).(*speedState)

    // CRITICAL: Do NOT reset old state here - it may still be read by other goroutines
    // Reset will happen on next Get() from pool, when it's safe

    // Put old state back in pool (safe - we're not modifying it)
    speedStatePool.Put(oldState)
}
```

### Why This Works

1. **After `Get()`**: No one has a reference to the state yet → Safe to reset
2. **After `Swap()`**: Old state may still be read by goroutines that `Load()`ed it → NOT safe to reset
3. **On next `Get()`**: State is retrieved from pool, no one reading it → Safe to reset

### Object Lifetime with Fix

```
1. Get() from pool
   └─> Reset() here ✅ (no one reading)

2. Initialize
   └─> Set values

3. Swap()
   └─> Old state returned
   └─> Old state may still be read by other goroutines

4. Put() back to pool
   └─> Do NOT reset here ❌ (others may be reading)
   └─> State has stale data, but that's OK

5. Next Get() from pool
   └─> Reset() here ✅ (no one reading yet)
   └─> Stale data cleared, ready for use
```

---

## Files to Update

1. **`internal/stats/client_stats.go`**:
   - `UpdateSpeed()` - Remove `oldState.Reset()` before `Put()` (line ~347)
   - `UpdateDrift()` - Remove `oldState.Reset()` before `Put()` (line ~297)
   - `RecordSegmentSize()` - Remove `oldState.Reset()` before `Put()` (line ~393)

2. **Update comments** to reflect the correct pattern

---

## Testing

After fix:
- ✅ `TestClientStats_ThreadSafety` should pass with `-race`
- ✅ All other tests should continue to pass
- ✅ No data races detected

---

---

## Alternative Solution: Use Atomics for Individual Fields

### The Simpler Approach

Instead of using `atomic.Value` with `sync.Pool` for the entire struct, we can use atomic operations for each individual field. This eliminates:
- ❌ The need for `sync.Pool` (no allocations/deallocations)
- ❌ The race condition (no struct swapping)
- ❌ The complexity of copy-on-write pattern
- ❌ The object lifetime management issues

### Implementation for `speedState`

**Current approach** (struct swap with pool):
```go
speedState atomic.Value // *speedState

func (s *ClientStats) UpdateSpeed(speed float64) {
    current := s.speedState.Load().(*speedState)
    newState := speedStatePool.Get().(*speedState)
    newState.Reset()
    // ... initialize ...
    oldState := s.speedState.Swap(newState).(*speedState)
    speedStatePool.Put(oldState)  // Race condition here!
}
```

**Alternative approach** (individual atomics):
```go
// Individual atomic fields - no struct, no pool needed
speed            atomic.Uint64 // math.Float64bits(speed)
belowThresholdAt atomic.Value  // time.Time

func (s *ClientStats) UpdateSpeed(speed float64) {
    // Update speed atomically (lock-free)
    s.speed.Store(math.Float64bits(speed))

    // Update belowThresholdAt based on speed
    currentSpeed := math.Float64frombits(s.speed.Load())
    if speed > 0 && speed < StallThreshold {
        // Speed is below threshold - check if we need to set timestamp
        if currentSpeed >= StallThreshold {
            // Just crossed below threshold - set timestamp
            s.belowThresholdAt.Store(time.Now())
        }
        // If already below threshold, keep existing timestamp
    } else {
        // Speed is above threshold - clear timestamp
        s.belowThresholdAt.Store(time.Time{})
    }
}

func (s *ClientStats) GetSpeed() float64 {
    return math.Float64frombits(s.speed.Load())
}

func (s *ClientStats) IsStalled() bool {
    thresholdTimePtr := s.belowThresholdAt.Load()
    if thresholdTimePtr == nil {
        return false
    }
    thresholdTime := thresholdTimePtr.(time.Time)
    if thresholdTime.IsZero() {
        return false
    }
    return time.Since(thresholdTime) > StallDuration
}
```

### Benefits

1. **No Race Conditions**: Each field is updated independently with atomic operations
2. **No Allocations**: No `sync.Pool`, no struct allocations, zero GC pressure
3. **Simpler Code**: No Reset() methods, no pool management, no object lifetime concerns
4. **Lock-Free**: All operations are atomic, no mutexes needed
5. **Better Performance**: Fewer memory operations, no struct copying

### Trade-offs

1. **Brief Out-of-Sync**: `speed` and `belowThresholdAt` can be momentarily out of sync
   - **Impact**: Minimal - worst case is a brief moment where speed is updated but timestamp isn't (or vice versa)
   - **Acceptable**: For stall detection, this brief inconsistency is acceptable
   - **Mitigation**: Update `speed` first, then `belowThresholdAt` (or vice versa based on logic)

2. **Ordering Consideration**: Need to think about update order
   - **Recommendation**: Update `speed` first, then `belowThresholdAt`
   - **Reason**: Speed is the primary value; timestamp is derived from speed changes

### Implementation for `driftState`

Similarly, we can convert `driftState` to individual atomics:

```go
// Individual atomic fields
lastPlaybackTime atomic.Int64 // time.Duration as nanoseconds
currentDrift     atomic.Int64 // time.Duration as nanoseconds
maxDrift         atomic.Int64 // time.Duration as nanoseconds

func (s *ClientStats) UpdateDrift(outTimeUS int64) {
    playbackTime := time.Duration(outTimeUS) * time.Microsecond
    wallClockElapsed := time.Since(s.StartTime)

    // Update atomically
    s.lastPlaybackTime.Store(int64(playbackTime))
    current := wallClockElapsed - playbackTime
    s.currentDrift.Store(int64(current))

    // Update max using CAS loop (like peakDropRate)
    for {
        oldMax := s.maxDrift.Load()
        if int64(current) <= oldMax {
            break
        }
        if s.maxDrift.CompareAndSwap(oldMax, int64(current)) {
            break
        }
    }
}
```

### Implementation for `segmentSizeState`

For the ring buffer, we have two options:

**Option 1**: Keep ring buffer but use atomics for index
```go
segmentSizes   []int64  // Shared slice (read-only after init)
segmentSizeIdx atomic.Int64  // Atomic index
```

**Option 2**: Use atomic operations for a simpler approach (if we don't need ring buffer)

### Recommendation

**✅ Use individual atomics instead of struct swap pattern**

**Rationale**:
1. **Simpler**: No pool management, no Reset() methods, no object lifetime concerns
2. **No race conditions**: Each field is independent
3. **Better performance**: No allocations, no struct copying
4. **Easier to maintain**: Less code, clearer intent

**Brief out-of-sync is acceptable**:
- For `speedState`: Speed updates frequently; brief inconsistency in timestamp is fine
- For `driftState`: All fields are updated together in `UpdateDrift()`; minimal inconsistency
- For `segmentSizeState`: Ring buffer updates are infrequent; brief inconsistency acceptable

---

## Conclusion

### Option 1: Fix Current Approach (Struct Swap)
- Remove `oldState.Reset()` before `Put()`
- Reset only on `Get()` from pool
- **Pros**: Keeps current design, reduces GC pressure
- **Cons**: Still complex, still has object lifetime management

### Option 2: Use Individual Atomics (Recommended) ✅
- Replace struct swap with individual atomic fields
- No `sync.Pool` needed
- **Pros**: Simpler, no race conditions, no allocations, better performance
- **Cons**: Brief out-of-sync possible (acceptable for use case)

**Recommendation**: **Use Option 2** - individual atomics are simpler, safer, and perform better. The brief out-of-sync is acceptable for these metrics.
