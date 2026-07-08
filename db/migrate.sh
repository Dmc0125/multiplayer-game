export PGPASSWORD=pwd

dir=$(dirname "$(realpath "$0")")

direction=$1
count=$2

$dir/migrator.bin $dir/migrations $1 $2

exit 0
