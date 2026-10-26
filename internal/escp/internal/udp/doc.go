// Package udp holds the one piece of socket handling the ESCP client needs that has nothing to do
// with the protocol: letting several sockets (several clients, Discover, other processes) bind the
// same reply port. The switch always answers to UDP 29809, whoever asked, so a second client for
// another switch, or a discovery next to a client, needs that port too.
//
// Share is a net.ListenConfig Control function. Where no sharing option is known it does nothing
// and the second bind fails loudly.
package udp
