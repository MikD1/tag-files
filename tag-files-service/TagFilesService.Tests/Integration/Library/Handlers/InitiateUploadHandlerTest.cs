using Microsoft.EntityFrameworkCore;
using Moq;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class InitiateUploadHandlerTest : InMemoryDatabaseTestBase
{
    [TestInitialize]
    public void SetupMocks()
    {
        Mock<IPresignedService> presignedMock = new();
        presignedMock
            .Setup(m => m.GenerateUploadUrl(It.IsAny<string>(), It.IsAny<string>(), It.IsAny<int>()))
            .ReturnsAsync("http://minio/upload-url");
        _handler = new(presignedMock.Object, DbContext);
    }

    [TestMethod]
    public async Task InitiateUpload_ShouldCreateProcessingFilesWithWaitingStatus()
    {
        InitiateUploadRequest request = new(["file1.jpg", "file2.jpg"]);

        await _handler.Handle(request, CancellationToken.None);

        List<ProcessingFile> files = await DbContext.ProcessingFiles.ToListAsync();
        Assert.AreEqual(2, files.Count);
        Assert.IsTrue(files.All(f => f.Status == ProcessingStatus.WaitingForUpload));
    }

    [TestMethod]
    public async Task InitiateUpload_ShouldSetCollectionIdOnProcessingFiles()
    {
        InitiateUploadRequest request = new(["file1.jpg", "file2.jpg"], CollectionId: 5u);

        await _handler.Handle(request, CancellationToken.None);

        List<ProcessingFile> files = await DbContext.ProcessingFiles.ToListAsync();
        Assert.IsTrue(files.All(f => f.CollectionId == 5u));
    }

    [TestMethod]
    public async Task InitiateUpload_ShouldReturnPresignedUrlForEachFile()
    {
        List<string> fileNames = ["photo.jpg", "video.mp4"];
        InitiateUploadRequest request = new(fileNames);

        Dictionary<string, string> result = await _handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(2, result.Count);
        Assert.IsTrue(result.ContainsKey("photo.jpg"));
        Assert.IsTrue(result.ContainsKey("video.mp4"));
    }

    [TestMethod]
    public async Task InitiateUpload_ShouldCreateProcessingFileWithUnknownFileType()
    {
        InitiateUploadRequest request = new(["photo.jpg"]);

        await _handler.Handle(request, CancellationToken.None);

        ProcessingFile file = await DbContext.ProcessingFiles.SingleAsync();
        Assert.AreEqual(FileType.Unknown, file.FileType);
    }

    private InitiateUploadHandler _handler = null!;
}
