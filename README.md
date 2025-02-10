# owncloud-upload-cleaner

If you sync your photos and videos from your phone automatically to owncloud you might end up with duplicates and zero byte files ([see bug](https://github.com/owncloud/android/issues/3983)). This is a tool that helps you clean up upload folders of owncloud.

This tool comes with a UI because that way it is more accessible to a broader audience. It also comes with a pre-built OSX version.

## Usage

```
Usage of ./builds/owncloud-upload-cleaner:
  -base_dir string
    	Directory that is cleaned, defaults to current working directory.
  -dry_run
    	Dry run, if enabled no files are deleted or moved.
  -mode string
    	Files will be moved or deleted depending on the mode (modes: move / delete). (default: move)
  -ui
    	Starts this tool in UI mode. (default true)
```

## Building

With the default golang tool chain run:

```
go build -o owncloud-upload-cleaner
```

In case you want to build an OSX application run:

```
fyne package -os darwin -name owncloud-upload-cleaner
```

More information for building the application for different OS can be found at [fyne documentation](https://developer.fyne.io/started/packaging).
