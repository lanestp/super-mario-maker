package joinpage

// MaxTries is the number of join tries that may still count for one address.
const MaxTries = 100

// MayJoin reports whether an address with this many counted tries may try to join once more.
func MayJoin(triesStillCounted int64) bool {
	return triesStillCounted < MaxTries
}
