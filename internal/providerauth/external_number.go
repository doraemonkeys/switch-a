package providerauth

// JSON itself permits larger integers, but the admin client consumes them as
// JavaScript numbers. Keeping source integers in this range prevents a previewed
// value from silently changing before the commit request returns to Go.
const (
	maximumJSONSafeInteger int64 = 1<<53 - 1
	minimumJSONSafeInteger int64 = -maximumJSONSafeInteger
)
