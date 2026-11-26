// Command fixtures writes sample data. It is a program, which nothing imports, so declaring testkit cannot keep it
// out of production.
//
//persona:layer testkit
package main

// Job is a record the program keeps.
type Job struct{ Name string }

func main() {}
