set -e
wd=`dirname $0`

folders=( $wd/ast $wd/io $wd )
for folder in ${folders[*]}
do
	go test -v -cover  -cpuprofile profile-binlog.out -bench=. -coverprofile=coverage-binlog.out $folder 
done

# Try go tool pprof profile-binlog.out 
# and go tool cover -html=coverage-binlog.out

