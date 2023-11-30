package ssh_server_test

import (
	"io"
	"io/ioutil"

	gossh "github.com/GoSeoTaxi/ssh_server"
	"golang.org/x/crypto/ssh"
)

func ExampleListenAndServe() {
	gossh.ListenAndServe(":2222", func(s gossh.Session) {
		io.WriteString(s, "Hello world\n")
	})
}

func ExamplePasswordAuth() {
	gossh.ListenAndServe(":2222", nil,
		gossh.PasswordAuth(func(ctx gossh.Context, pass string) bool {
			return pass == "secret"
		}),
	)
}

func ExampleNoPty() {
	gossh.ListenAndServe(":2222", nil, gossh.NoPty())
}

func ExamplePublicKeyAuth() {
	gossh.ListenAndServe(":2222", nil,
		gossh.PublicKeyAuth(func(ctx gossh.Context, key gossh.PublicKey) bool {
			data, _ := ioutil.ReadFile("/path/to/allowed/key.pub")
			allowed, _, _, _, _ := ssh.ParseAuthorizedKey(data)
			return gossh.KeysEqual(key, allowed)
		}),
	)
}

func ExampleHostKeyFile() {
	gossh.ListenAndServe(":2222", nil, gossh.HostKeyFile("/path/to/host/key"))
}
