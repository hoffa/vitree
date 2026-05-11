#!/bin/sh
set -eux

tag=$1
git tag "$tag"
git push origin "$tag"
