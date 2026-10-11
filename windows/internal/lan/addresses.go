// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

package lan

import (
	"fmt"
	"net"
	"slices"
)

// routeProbe is an address of TEST-NET-1 (RFC 5737): it is never used on a
// real network. "Dialling" it over UDP sends nothing; it only makes Windows
// pick the connection it would use, whose address is the main one.
const routeProbe = "192.0.2.1:9"

// Addresses returns the addresses under which the devices of the home
// network reach the server, such as "http://192.168.1.20:8080/" — the
// address of the main connection first.
func Addresses(port int) []string {
	var ips []net.IP
	if main := mainAddress(); main != nil {
		ips = append(ips, main)
	}
	for _, ip := range interfaceAddresses() {
		if !slices.ContainsFunc(ips, ip.Equal) {
			ips = append(ips, ip)
		}
	}
	urls := make([]string, len(ips))
	for i, ip := range ips {
		urls[i] = fmt.Sprintf("http://%s:%d/", ip, port)
	}
	return urls
}

// mainAddress returns the address of the connection Windows uses to reach
// other networks, or nil if it is not a home network address.
func mainAddress() net.IP {
	conn, err := net.Dial("udp4", routeProbe)
	if err != nil {
		return nil
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || !homeAddress(addr.IP) {
		return nil
	}
	return addr.IP.To4()
}

// interfaceAddresses lists the home network addresses of all connections
// that are up.
func interfaceAddresses() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && homeAddress(ipNet.IP) {
				ips = append(ips, ipNet.IP.To4())
			}
		}
	}
	return ips
}

// homeAddress: home networks use the private IPv4 ranges (192.168.x.x,
// 10.x.x.x, 172.16.x.x to 172.31.x.x).
func homeAddress(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4.IsPrivate()
}
