#!/usr/bin/env bash

REGISTRY=datarobot
IMG=install-prereqchecker
TAG=latest

docker buildx build --platform linux/amd64 -t $REGISTRY/$IMG:$TAG .
docker push $REGISTRY/$IMG:$TAG