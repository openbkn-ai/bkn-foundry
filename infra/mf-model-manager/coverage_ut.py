import os

# The service now defaults to AUTH_ENABLED=true so a deployment that forgets the
# variable fails closed. The suite predates that default and asserts the
# anonymous-allow behaviour, so pin the open value here instead of rewriting
# every case; tests that exercise the auth path patch base_config themselves.
os.environ.setdefault("AUTH_ENABLED", "false")

import unittest
from os import path

import coverage
import xmlrunner

# Instantiate a coverage object.
cov = coverage.coverage()
cov.start()

# Test suite.
test_dir = path.join(path.dirname(path.abspath(__file__)), 'app/test')

suite = unittest.defaultTestLoader.discover(test_dir, "test_*.py")
# unittest.TextTestRunner().run(suite)
runner = xmlrunner.XMLTestRunner(output="coverage_result")  # Test result report.
runner.run(suite)

# Stop analysis.
cov.stop()

# Save results.
cov.save()

# Display results in command-line mode.
cov.report()

# Generate the HTML coverage report.
# cov.html_report(directory='result_html')
cov.xml_report(outfile="coverage.xml")  # Code coverage report.
