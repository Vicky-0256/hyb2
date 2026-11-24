# Local entry points for the commands CI runs. `make help` lists them.
# Override the interpreter with: make test PYTHON=/path/to/python3
PYTHON ?= python3
QUICKSTART_OUT ?= out/quickstart

.DEFAULT_GOAL := help
.PHONY: help install quickstart test test-pipeline test-desktop test-cplfold test-go verify build-web clean

help:
	@echo "make install        pip install cplfold and the hyb2pipe/hyb2analysis package"
	@echo "make quickstart     run hyb2pipe on data/testData.sam into $(QUICKSTART_OUT)"
	@echo "make test           test-pipeline, test-desktop, test-cplfold and test-go"
	@echo "make test-pipeline  hyb2pipe and hyb2analysis tests, including the golden files"
	@echo "make test-desktop   desktop Python runtime tests"
	@echo "make test-cplfold   CPLfold snapshot check and tests"
	@echo "make test-go        desktop app tests (go vet and go test)"
	@echo "make build-web      build the browser ViennaRNA and CPLfold runtimes into web/vendor/"
	@echo "make verify         browser release gate and generated-asset check"
	@echo "make clean          remove $(QUICKSTART_OUT) and Python caches"

install:
	$(PYTHON) -m pip install ./third_party/cplfold ./pipeline

quickstart:
	mkdir -p $(dir $(QUICKSTART_OUT))
	PYTHONPATH=pipeline $(PYTHON) -m hyb2pipe -i data/testData.sam -d data/Zika_18S.fasta -o $(QUICKSTART_OUT) -e 1
	@echo "Wrote $(QUICKSTART_OUT).hyb, $(QUICKSTART_OUT)_homodimers.hyb and $(QUICKSTART_OUT)_hybrids.hyb_stats.txt"

test: test-pipeline test-desktop test-cplfold test-go

test-pipeline:
	$(PYTHON) -m unittest discover -s pipeline/tests -p 'test_*.py'

test-desktop:
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m unittest discover -s desktop/runtime/tests -p 'test_*.py'

test-cplfold:
	$(PYTHON) scripts/verify/verify-cplfold-python.py

test-go:
	cd desktop/app && go vet ./... && go test ./...

# As .github/workflows/deploy-pages.yml; needs Emscripten, Node and Python 3.13 (web/README.md).
build-web:
	bash scripts/build/build-vienna-wasm.sh
	HYB2_PYTHON313="$$(command -v python3.13 || command -v python3)" bash scripts/build/build-vienna-pyodide.sh
	bash scripts/build/build-cplfold-web.sh

verify:
	$(PYTHON) -B scripts/build/export-support-kernels.py --check
	bash scripts/verify/verify-browser-release.sh

clean:
	rm -f $(QUICKSTART_OUT).* $(QUICKSTART_OUT)_*
	find pipeline desktop/runtime third_party/cplfold scripts -name __pycache__ -type d -prune -exec rm -rf {} +
