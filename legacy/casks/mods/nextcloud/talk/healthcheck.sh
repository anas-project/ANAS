#!/bin/bash

nc -z localhost 8081 || exit 1
nc -z localhost 8188 || exit 1
nc -z localhost 4222 || exit 1
