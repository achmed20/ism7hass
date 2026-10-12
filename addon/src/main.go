// Testing Packages, no cover by 'go get'
//go:generate go get github.com/stretchr/testify/assert
//go:generate go get github.com/h2non/gock

//go:debug x509negativeserial=1

package main

import "ism7/cmd"

var Version string
var Branch string
var Commit string

var Built string

func main() {

	cmd.Execute()
}
