module github.com/shengyanli1982/conecta/examples/simple

go 1.19

replace github.com/shengyanli1982/conecta => ../../

require (
	github.com/google/uuid v1.6.0
	github.com/shengyanli1982/conecta v0.0.0-20211013143910-5f9b8b8b2b0e
	github.com/shengyanli1982/workqueue/v2 v2.2.6
)

require golang.org/x/time v0.5.0 // indirect
