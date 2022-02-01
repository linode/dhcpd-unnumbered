# dhcpd-unnumbered

### what is dhcpd-unnumbered
dhcpd-unnumbered is a very light weight ipv4 dhcp server designed for unnumbered l3 tap interfaces

### how does it work
- it listens for dhcp requests on all interfaces (so dynamic tap interfaces can come and go without changes)
- incoming requests are matched to their interface
- the interface is checked against a regex. only matching interfaces are handled (default tap.*_0), not matching are ignored completely
- if tap matches
	- routes for that tap are looked up,
	- if client requested a specific IP *and* still owns this IP, that IP is offered
	- if client did not request an IP (aka DHCP discover) the first *non-private* IP is being offered

### NOTES:
- dhcp offers will supply a fake /24, clients are let to believe that they live in a shared /24 subnet
- dhcp will include/offer a gateway IP using the first IP in the clients "fake" /24
- the dhcp can/will include a hostname
  different options can be supported around this:
  - static hostname (every client gets the same hostname)
  - dynamic hostname: hostname is generated from its IP, with the dots replaced with -
  - hostname override: dhcpd-unnumbered can dynamically pick up a file reading the hostname from it. completely customized hostnames can be offered through this
- dhcpd-unnumbered can also offer a tftp next-host IP for pxebooting clients


### usage:
```
dhcpd-unnumbered --help
```
