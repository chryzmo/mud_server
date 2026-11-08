package worldfile

// Colors and Badges are the fixed sets the client can draw. Content names one by key, never CSS or
// text, so content cannot inject styling and every colour can be contrast-tested in the client.
// A rule's unlock effect grants one (rules.go); the player picks what to wear with COSMETIC.
var (
	Colors = []string{"gold", "crimson", "azure", "emerald", "violet", "amber", "rose", "silver"}
	Badges = []string{"star", "crown", "key", "lantern", "bell", "gear", "quill", "flame"}
)
