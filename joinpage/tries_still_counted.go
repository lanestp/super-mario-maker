package joinpage

// JoinWindowMilliseconds is how long a join try counts against an address.
const JoinWindowMilliseconds int64 = 3600000

// TriesStillCounted returns the tries that are less than one window old at now,
// including tries dated after now, in their original order with repeats kept.
func TriesStillCounted(times []int64, now int64) []int64 {
	out := make([]int64, 0, len(times))
	const minInt64 = -1 << 63
	tooLow := now < minInt64+JoinWindowMilliseconds
	threshold := int64(0)
	if !tooLow {
		threshold = now - JoinWindowMilliseconds
	}
	for _, t := range times {
		if tooLow || t > threshold {
			out = append(out, t)
		}
	}
	return out
}
