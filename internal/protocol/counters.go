package protocol

// Delta 按项计算增量 max(0, 新 - 旧)；任一项变小时 regressed 为 true。
func Delta(old, cur Counters) (d Counters, regressed bool) {
	sub := func(o, n uint64) uint64 {
		if n < o {
			regressed = true
			return 0
		}
		return n - o
	}
	d = Counters{
		Sessions:    sub(old.Sessions, cur.Sessions),
		FramesOK:    sub(old.FramesOK, cur.FramesOK),
		DtMs:        sub(old.DtMs, cur.DtMs),
		MonoMs:      sub(old.MonoMs, cur.MonoMs),
		RunOK:       sub(old.RunOK, cur.RunOK),
		GatedFrames: sub(old.GatedFrames, cur.GatedFrames),
		GatedRuns:   sub(old.GatedRuns, cur.GatedRuns),
	}
	return d, regressed
}

// MaxCounters 按项取较大值，已确认counters只增不减。
func MaxCounters(a, b Counters) Counters {
	return Counters{
		Sessions:    max(a.Sessions, b.Sessions),
		FramesOK:    max(a.FramesOK, b.FramesOK),
		DtMs:        max(a.DtMs, b.DtMs),
		MonoMs:      max(a.MonoMs, b.MonoMs),
		RunOK:       max(a.RunOK, b.RunOK),
		GatedFrames: max(a.GatedFrames, b.GatedFrames),
		GatedRuns:   max(a.GatedRuns, b.GatedRuns),
	}
}
