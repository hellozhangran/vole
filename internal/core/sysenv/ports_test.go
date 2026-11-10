package sysenv

import "testing"

func TestParsePorts(t *testing.T) {
	out := "\nHardware Port: Ethernet Adapter (en4)\nDevice: en4\nEthernet Address: 1e:5b:79:e7:1b:41\n\n" +
		"Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: f8:4d:89:60:ba:24\n\nVLAN Configurations\n===================\n"
	got := parsePorts(out)
	if len(got) != 2 || got["en0"] != "Wi-Fi" || got["en4"] != "Ethernet Adapter (en4)" {
		t.Fatalf("%v", got)
	}
}
