cd `dirname "$0"`
set -e
go get "github.com/larytet-go/moduledata"
go get "github.com/larytet-go/procfs"
go get "github.com/larytet-go/procfs/maps"
go tool compile -I $GOPATH/pkg/linux_amd64 -S binlog.go
#go build binlog.go
