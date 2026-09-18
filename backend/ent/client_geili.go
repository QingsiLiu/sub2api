package ent

// InTransaction reports an Ent-owned transaction without opening a nested one.
// Kept outside generated files so repository helpers can accept tx.Client().
func (c *Client) InTransaction() bool {
	_, ok := c.driver.(*txDriver)
	return ok
}
