package main

import (
	"context"
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	bs "github.com/inhies/go-bytesize"

	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type Action int

func (a Action) ToString() string {
	switch a {
	case NoAction:
		return "keep"
	case MoveAction:
		return "move"
	case DeleteAction:
		return "delete"
	}
	return "UnknownAction"
}

const (
	NoAction Action = iota
	DeleteAction
	MoveAction
)

type Reason int

func (r Reason) ToString() string {
	switch r {
	case ZeroByteReason:
		return "Zero Bytes"
	case DuplicateReason:
		return "Duplicate"
	}
	return "UnknownReason"
}

const (
	NoReason Reason = iota
	ZeroByteReason
	DuplicateReason
)

type File struct {
	FileInfo         fs.FileInfo
	MD5Sum           string
	FileName         string
	FilePath         string
	OriginalFileName string
	OriginalFilePath string
	Action           Action
	Reason           Reason
}

// Flag values
var baseDir string
var dryRun bool
var modeFlag string
var mode Action
var ui bool

// accounting
var evaluatedFileCounter = 0
var uniqueKeepFiles = 0
var emptyFileCounter = 0
var duplicateFileCounter = 0
var processedFileCounter = 0
var skippedDirectoryCounter = 0
var skippedExtensionFilesCounter = 0
var totalFileSizeCounter = 0
var processedFileSizeCounter = 0
var allMD5 = map[string]File{}

func uiSetupContent(ctx context.Context) fyne.App {
	a := app.New()
	a.Settings().SetTheme(theme.DefaultTheme())
	w := a.NewWindow("Owncloud Folder Cleanup")

	descriptionText := widget.NewLabel("This tool cleans a directory from duplicate or empty files.\nIt handles the following file formats:\n\t- jpg\n\t- jpeg\n\t- mpg\n\t- mp4")

	folderSelectionLabel := canvas.NewText("Directory:", color.Black)
	folderPathInput := widget.NewEntry()
	folderPathInput.SetPlaceHolder("Select your directory...")
	folderPathInput.OnChanged = func(s string) {
		baseDir = s
	}
	folderSelectionButton := widget.NewButton("Choose directory", func() {
		fWindow := a.NewWindow("Choose folder to cleanup")
		filePicker := dialog.NewFolderOpen(func(list fyne.ListableURI, err error) {
			if list != nil {
				baseDir = list.Path()
				folderPathInput.SetText(baseDir)
			}
			fWindow.Close()
		}, fWindow)
		fWindow.Resize(fyne.NewSize(520, 400))
		filePicker.Resize(fyne.NewSize(520, 400))
		fWindow.Content().Refresh()
		fWindow.Show()
		filePicker.Show()
	})

	dryRun = false
	dryRunCheck := widget.NewCheck("Dry run", func(value bool) {
		fmt.Println("dry_run:", value)
		dryRun = value
	})
	mode = DeleteAction
	modeRadio := widget.NewRadioGroup([]string{"Move", "Delete"}, func(value string) {
		fmt.Println("mode:", value)
		modeFlag = strings.ToLower(value)
		if modeFlag == "move" {
			mode = MoveAction
		}
		if modeFlag == "delete" {
			mode = DeleteAction
		}
	})
	modeRadio.SetSelected("Delete")

	emptyLabel := container.New(layout.NewCenterLayout(), widget.NewLabel(" "))
	formContainer := container.New(layout.NewFormLayout(), folderSelectionLabel, folderPathInput, emptyLabel, folderSelectionButton, emptyLabel, dryRunCheck, emptyLabel, modeRadio)

	// progress bar
	progress := 0.0
	progressBinding := binding.BindFloat(&progress)
	progressBar := widget.NewProgressBarWithData(progressBinding)
	progressBar.TextFormatter = func() string { return fmt.Sprintf("%.1f%% done", progress*100) }

	// text output
	txtEntries := []string{"Please select a directory for cleanup and click start."}
	txtEntriesList := widget.NewList(
		func() int { return len(txtEntries) },
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(txtEntries[i])
		},
	)
	txtEntriesContainer := container.New(layout.NewGridWrapLayout(fyne.NewSize(500, 300)), txtEntriesList)

	var startButton *widget.Button
	startButton = widget.NewButton("Start", func() {
		startButton.Disable()
		progress = 0
		progressBinding.Reload()
		files, err := scanFiles(baseDir)
		if err != nil {
			dialog.ShowInformation("Error", err.Error(), w)
			return
		}
		txtEntries = []string{}
		evaluatedChannel := make(chan File, 100)
		processedChannel := make(chan File, 100)
		go evaluateFiles(ctx, files, mode, evaluatedChannel)
		go processFiles(ctx, evaluatedChannel, processedChannel)
		go func(inChannel chan File) {
			for {
				select {
				case <-ctx.Done():
					return
				case f, more := <-inChannel:
					if !more {
						startButton.Enable()
						txtEntries = append(txtEntries, "======== DONE =========")
						txtEntriesList.Refresh()
						txtEntriesList.ScrollToBottom()
						return
					}
					// set progress status
					progress = float64(evaluatedFileCounter) / float64(len(files))
					progressBinding.Reload()
					// add description to status text
					action := describeAction(f, dryRun)
					fmt.Print(action)
					idx := len(txtEntries)
					txtEntriesList.SetItemHeight(idx, float32(strings.Count(action, "\n"))*30)
					txtEntries = append(txtEntries, action)
					txtEntriesList.Refresh()
					txtEntriesList.ScrollToBottom()
				}
			}
		}(processedChannel)
	})
	closeButton := widget.NewButton("Cancel", func() {
		ctx.Done()
		a.Quit()
	})
	gridButtons := container.New(layout.NewAdaptiveGridLayout(2), closeButton, startButton)
	contentGrid := container.New(layout.NewVBoxLayout(), emptyLabel, formContainer /*gridChecks,*/, layout.NewSpacer(), progressBar, txtEntriesContainer)
	mainGrid := container.New(layout.NewBorderLayout(descriptionText, gridButtons, nil, nil), descriptionText, gridButtons, contentGrid)
	w.SetContent(mainGrid)
	w.SetCloseIntercept(func() {
		os.Exit(0)
	})
	w.Show()
	return a
}

func scanFiles(dir string) ([]fs.FileInfo, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	matchingFiles := []fs.FileInfo{}
	regexSupportedType := regexp.MustCompile(`\.(jpg|jpeg|dng|mpg|mp4)$`)
	for _, f := range files {
		if f.IsDir() {
			skippedDirectoryCounter++
			fmt.Println("skip dir:", f.Name())
			continue
		}
		file, err := f.Info()
		if err != nil {
			return nil, err
		}
		if !regexSupportedType.MatchString(file.Name()) {
			skippedExtensionFilesCounter++
			fmt.Println("skip file:", f.Name())
			continue
		}
		matchingFiles = append(matchingFiles, file)
	}
	sort.Slice(matchingFiles, func(i, j int) bool {
		return len(matchingFiles[i].Name()) < len(matchingFiles[j].Name())
	})
	return matchingFiles, nil
}

func createMoveFolder(parentDir string) (string, error) {
	path := filepath.Join(parentDir, "moved")
	err := os.MkdirAll(path, 0750)
	return path, err
}

func evaluateFiles(ctx context.Context, files []fs.FileInfo, mode Action, outChannel chan File) error {
	for _, file := range files {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		fp := filepath.Join(baseDir, file.Name())
		// calculate hash
		md5 := MD5Sum(fp)
		f := File{
			FileInfo:         file,
			MD5Sum:           md5,
			FileName:         file.Name(),
			FilePath:         fp,
			OriginalFileName: file.Name(),
			OriginalFilePath: fp,
			Action:           NoAction,
			Reason:           NoReason,
		}
		if file.Size() == 0 {
			f.Action = mode
			f.Reason = ZeroByteReason
			emptyFileCounter++
		}

		// check if file with same MD5 sum already exists
		orig, exists := allMD5[md5]
		if exists && file.Size() != 0 {
			f.Action = mode
			f.Reason = DuplicateReason
			f.OriginalFileName = orig.FileName
			f.OriginalFilePath = orig.FilePath
			duplicateFileCounter++
		} else {
			uniqueKeepFiles++
			allMD5[md5] = f
		}
		evaluatedFileCounter++
		totalFileSizeCounter += int(file.Size())
		outChannel <- f
	}
	close(outChannel)
	return nil
}

// processFiles processes each file received in the inChannel and
// performs the action according to the instructions.
func processFiles(ctx context.Context, inChannel chan File, outChannel chan File) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case f, more := <-inChannel:
			if !more {
				close(outChannel)
				return nil
			}
			err := processFile(f)
			if err != nil {
				return err
			}
			outChannel <- f
		}
	}
}

func processFile(f File) error {
	dir := filepath.Dir(f.FilePath)
	filename := filepath.Base(f.FilePath)
	var err error

	switch f.Action {
	case MoveAction:
		processedFileCounter++
		processedFileSizeCounter += int(f.FileInfo.Size())
		var dest string
		dest, err = createMoveFolder(dir)
		if err != nil {
			break
		}
		destFile := filepath.Join(dest, filename)
		if dryRun {
			break
		}
		err = os.Rename(f.FilePath, destFile)
	case DeleteAction:
		processedFileCounter++
		processedFileSizeCounter += int(f.FileInfo.Size())
		if dryRun {
			break
		}
		err = os.Remove(f.FilePath)
	case NoAction:
		break
	}
	return err
}

func describeAction(f File, dryRun bool) string {
	var s strings.Builder
	s.WriteString(fmt.Sprintf("[%s] %s [%s]", f.Action.ToString(), f.FileName, bs.ByteSize(f.FileInfo.Size())))
	if f.Action != NoAction {
		s.WriteString(fmt.Sprintf("\n - %s", f.Reason.ToString()))
		if f.Reason == DuplicateReason {
			s.WriteString(fmt.Sprintf(" of %s", f.OriginalFileName))
		}
	}
	s.WriteString("\n")
	return s.String()
}

func describeSummary() string {
	var s strings.Builder
	s.WriteString("\nSummary:\n---------------\n")
	s.WriteString(fmt.Sprintf("skipped %d directories and %d files.\n", skippedDirectoryCounter, skippedExtensionFilesCounter))
	s.WriteString(fmt.Sprintf("%d files evaluated\n", evaluatedFileCounter))
	s.WriteString(fmt.Sprintf(" - %d empty files\n", emptyFileCounter))
	s.WriteString(fmt.Sprintf(" - %d duplicate files\n", duplicateFileCounter))
	s.WriteString(fmt.Sprintf(" - %d unique files to keep\n", uniqueKeepFiles))
	processedSize := bs.ByteSize(processedFileSizeCounter) * bs.B
	totalSize := bs.ByteSize(totalFileSizeCounter) * bs.B
	s.WriteString(fmt.Sprintf(" - %s cleaned of %s total\n", processedSize.String(), totalSize.String()))
	if dryRun {
		s.WriteString(" - DRY RUN\n")
	}
	return s.String()
}

func MD5Sum(filepath string) string {
	file, err := os.Open(filepath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func parseFlags() {
	flag.StringVar(&baseDir, "base_dir", "", "Directory that is cleaned, defaults to current working directory.")
	flag.BoolVar(&dryRun, "dry_run", false, "Dry run, if enabled no files are deleted.")
	flag.StringVar(&modeFlag, "mode", MoveAction.ToString(), "Files will be moved or deleted depending on the mode (modes: move / delete).")
	flag.BoolVar(&ui, "ui", true, "Starts this tool in UI mode.")
	flag.Parse()

	switch modeFlag {
	case MoveAction.ToString():
		mode = MoveAction
	case DeleteAction.ToString():
		mode = DeleteAction
	default:
		log.Fatalln("Unsupported mode provided.")
	}
}

func main() {
	parseFlags()
	ctx := context.Background()

	// Run in UI mode
	if ui {
		a := uiSetupContent(ctx)
		a.Run()
		return
	}
	// Run in CLI mode
	pwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if baseDir == "" {
		baseDir = pwd
	}
	fmt.Println("Scanning files in:", baseDir)
	fmt.Println(" - dry_run:", dryRun)
	fmt.Println(" - mode:", mode.ToString())

	// scan files
	files, err := scanFiles(baseDir)
	if err != nil {
		log.Fatal(err)
	}

	var wg sync.WaitGroup
	evaluatedChannel := make(chan File, 100)
	processedChannel := make(chan File, 100)
	go evaluateFiles(ctx, files, mode, evaluatedChannel)
	go processFiles(ctx, evaluatedChannel, processedChannel)
	wg.Add(1)
	go func(inChannel chan File) {
		for {
			select {
			case <-ctx.Done():
				wg.Done()
				return
			case f, more := <-inChannel:
				if !more {
					wg.Done()
					return
				}
				action := describeAction(f, dryRun)
				fmt.Print(action)
			}
		}
	}(processedChannel)
	wg.Wait()
	fmt.Print(describeSummary())
}
