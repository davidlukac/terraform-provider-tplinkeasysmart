package escp

import (
	"strconv"
	"testing"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Several clients (several provider instances, one per switch) must be able to
// hold the reply port at the same time.
func TestSeveralClientsShareTheReplyPort(t *testing.T) {
	port := freeUDPPort(t)
	var clients []*Client
	for i := 0; i < 3; i++ {
		c, err := Dial(Config{Host: "127.0.0.1", HostPort: port})
		if err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		err = c.open()
		c.mu.Unlock()
		if err != nil {
			t.Fatalf("client %d could not share port %d: %v", i+1, port, err)
		}
		clients = append(clients, c)
	}
	for _, c := range clients {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
