wd=`dirname $0`
go test -v -cover  -cpuprofile profile-binlog.out -bench=. -coverprofile=coverage-binlog.out $wd 
# go test -cover  -cpuprofile profile-io.out -bench=. -coverprofile=coverage-io.out $wd/io
# Try go tool pprof profile-binlog.out 
# and go tool cover -html=coverage-binlog.out

