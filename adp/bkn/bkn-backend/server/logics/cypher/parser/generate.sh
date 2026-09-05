#!/bin/sh
# Copyright openbkn.ai
# Copyright The kweaver.ai Authors.
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

# The generator version must match the antlr4-go runtime pinned in go.mod.
# Fetch the jar from Maven Central; www.antlr.org is unreachable from some networks:
#   curl -sSLO https://repo1.maven.org/maven2/org/antlr/antlr4/4.13.1/antlr4-4.13.1-complete.jar
#   mv antlr4-4.13.1-complete.jar antlr-4.13.1-complete.jar

alias antlr4='java -Xmx500M -cp "./antlr-4.13.1-complete.jar:$CLASSPATH" org.antlr.v4.Tool'
antlr4 -Dlanguage=Go -no-listener -visitor -package parsing -o ../parsing Cypher.g4
