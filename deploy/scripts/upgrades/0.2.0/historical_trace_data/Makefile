.PHONY: test lint build ci

test:
	python3 -m unittest discover -s . -p 'test_*.py'
	$(MAKE) -C span-codec test

lint:
	python3 -m compileall -q .
	$(MAKE) -C span-codec lint

build:
	$(MAKE) -C span-codec build

ci: lint test build
