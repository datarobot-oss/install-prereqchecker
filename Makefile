# Dev environment setup
install-deps:  ## Install dev requirements
	go clean -cache -modcache && go mod tidy

run-tests:  ## Run tests without Sonobuoy
	go clean -testcache && go test -v ./pkg/
