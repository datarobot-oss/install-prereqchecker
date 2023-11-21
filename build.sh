#!/usr/bin/env bash

REGISTRY=antonpolyakov
IMG=sonobuyo
TAG=v0.0.1

docker build . -t $REGISTRY/$IMG:$TAG
docker push $REGISTRY/$IMG:$TAG