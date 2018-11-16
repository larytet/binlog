wd=`dirname $0`
go test -cover  -cpuprofile profile-binlog.out -bench=. -coverprofile=coverage-binlog.out $wd 
# Try go tool cover -html=coverage-binlog.out
# go tool pprof profile-binlog.out 

