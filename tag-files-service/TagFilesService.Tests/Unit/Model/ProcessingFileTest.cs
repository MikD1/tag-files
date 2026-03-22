using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class ProcessingFileTest
{
    [DataTestMethod]
    [DataRow("video-1.mp4")]
    [DataRow("video-2.avi")]
    [DataRow("video-3.webm")]
    public void VideoFile_ShouldInitializeCorrect(string fileName)
    {
        ProcessingFile processingFile = new(fileName, FileType.Video);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(".mp4"));
        Assert.AreEqual(FileType.Video, processingFile.FileType);
    }

    [DataTestMethod]
    [DataRow("photo-1.jpg", ".jpg")]
    [DataRow("photo-2.png", ".png")]
    public void ImageFile_ShouldInitializeCorrect(string fileName, string expectedLibraryFileExtensin)
    {
        ProcessingFile processingFile = new(fileName, FileType.Image);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(expectedLibraryFileExtensin));
        Assert.AreEqual(FileType.Image, processingFile.FileType);
    }

    [DataTestMethod]
    [DataRow("photo.jpg", null)]
    [DataRow("photo.jpg", 42u)]
    public void WaitingForUpload_ShouldInitializeCorrect(string fileName, uint? collectionId)
    {
        ProcessingFile processingFile = new(fileName, collectionId);

        Assert.AreEqual(fileName, processingFile.OriginalFileName);
        Assert.AreEqual(FileType.Unknown, processingFile.FileType);
        Assert.AreEqual(ProcessingStatus.WaitingForUpload, processingFile.Status);
        Assert.AreEqual(collectionId, processingFile.CollectionId);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(Path.GetExtension(fileName)));
    }

    [DataTestMethod]
    [DataRow("clip.mov")]
    [DataRow("clip.webm")]
    public void SetFileType_ToVideo_ShouldUpdateLibraryFileNameToMp4(string fileName)
    {
        ProcessingFile processingFile = new(fileName);

        processingFile.SetFileType(FileType.Video);

        Assert.AreEqual(FileType.Video, processingFile.FileType);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(".mp4"));
    }

    [DataTestMethod]
    [DataRow("photo.jpg", ".jpg")]
    [DataRow("photo.png", ".png")]
    public void SetFileType_ToImage_ShouldKeepOriginalExtension(string fileName, string expectedExtension)
    {
        ProcessingFile processingFile = new(fileName);

        processingFile.SetFileType(FileType.Image);

        Assert.AreEqual(FileType.Image, processingFile.FileType);
        Assert.IsTrue(processingFile.LibraryFileName.EndsWith(expectedExtension));
    }
}