package main
import (
	"fmt"
	"golang.org/x/crypto/ssh"
)
func main() {
	config := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{ssh.Password("2212427")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, _ := ssh.Dial("tcp", "192.168.37.130:22", config)
	defer client.Close()
	session, _ := client.NewSession()
	defer session.Close()
	out, _ := session.CombinedOutput("grep -E 'Backup|completed' /www/server/cron/*.log | head -n 20")
	fmt.Println(string(out))
}
