mkdir -p dist
rm -rf dist/*

platforms=("windows" "darwin" "linux")

for platform in "${platforms[@]}"
do
    if [ $platform == "windows" ]; then
        GOOS="${platform}" GOARCH=amd64 go build -o "dist/PM-StorageExtAPI-${platform}-amd64.exe"
        GOOS="${platform}" GOARCH=arm64 go build -o "dist/PM-StorageExtAPI-${platform}-arm64.exe"
    else
        GOOS="${platform}" GOARCH=amd64 go build -o "dist/PM-StorageExtAPI-${platform}-amd64"
        GOOS="${platform}" GOARCH=arm64 go build -o "dist/PM-StorageExtAPI-${platform}-arm64"
    fi
done