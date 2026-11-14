package github

// signedInAs puts token in memory as one GitHub has named the account of already, for a fake GitHub that serves
// no /user: what is read as an account (the thread cache, the inbox) asks whose a new token is first.
func signedInAs(c *Connector, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newCredential(token)
	c.acct.login, c.acct.known = "example-user", true
}
