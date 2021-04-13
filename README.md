# my-dhcpd

### what is my-dhcpd
my-dhcpd is a very light weight ipv4 dhcp server designed for unnumbered l3 tap interfaces

### how does it work
- it listens for dhcp requests on all interfaces (so dynamic tap interfaces can come and go without changes)
- incoming requests are matched against a interface regex. only matching interfaces are handled (default tap.*_0)
- if tap matches
	- routes for that tap are looked up,
	- if client requested a specific IP *and* still owns this IP, that IP is offered
	- if client did not request an IP the first non-private IP is being offered

	- dhcp offers will fake a /24
	- dhcp will include a gateway IP using the IP offered, replacing the last octet with a .1
	- the dhcp will include a hostname (right now static, but considering to change this to some uniqe value like timestamp)



### usage:
```
Usage of ./my-dhcpd:
  -hostname string
        hostname to be handed out in dhcp offeres (default "dyanmic-hostname")
  -leasetime duration
        Lease time in minutes. (default 24h)
  -loglevel string
        Log level. One of [fatal none trace debug info warning error] (default "info")
  -pvtcidr string
        private IP range. this IP CIDR will not be used for DHCP leases (default "192.168.0.0/16")
  -regex string
        regex to match interfaces. (default "tap.*_0")

```
