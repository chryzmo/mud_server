package mudserver

type Item struct {
	Name string
	Quantity int
	Persistent bool
}

// Create a copy of the item.  May be useful for breaking up stacks
func (i Item) createCopy() (*Item) {
	return &Item{
		Name: i.Name,
		Quantity: i.Quantity,
		Persistent: i.Persistent,
	}
}