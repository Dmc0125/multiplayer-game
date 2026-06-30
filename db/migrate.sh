export PGPASSWORD=pwd

dir=$(dirname "$(realpath "$0")")

direction="up"
user="user"
db="game" 
host="localhost"
port="5432"

while [[ $# -gt 0 ]]; do
    case $1 in
        up|down) direction=$1;;
        -u|--user) user=$2; shift;;
        -d|--db) db=$2; shift;;
        -h|--host) host=$2; shift;;
        -p|--port) port=$2; shift;;
        -pw|--password) password=$2; shift;;
        *) echo "Unknown argument: $1"; exit 1;;
    esac
    shift
done

echo "Migrating $direction... (user=$user, db=$db, host=$host, port=$port)"

if [[ $direction == "down" ]]; then
    psql -U $user -d $db -h $host -p $port -f $dir/migrate.down.sql
else
    psql -U $user -d $db -h $host -p $port -f $dir/migrate.up.sql
fi

exit 0
