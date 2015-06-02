# This tests various expected error scenarios when pulling bad content

host="localhost:6666"
base="hello-world"

function setup() {
	docker pull $base:latest
}

@test "Bad signature" {
	docker tag -f $base:latest $host/$base:badsignature
	run docker push $host/$base:badsignature
	[ "$status" -eq 0 ]

	run docker pull $host/$base:badsignature
	[ "$status" -ne 0 ]
}

@test "Wrong image name" {
	docker tag -f $base:latest $host/$base:rename
	run docker push $host/$base:rename
	[ "$status" -eq 0 ]

	run docker pull $host/$base:rename
	skip "Incorrect in docker 1.7"
	#[ "$status" -ne 0 ]
}

