using Microsoft.EntityFrameworkCore;
using TagFilesService.Library;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library;

[TestClass]
public class MetadataServiceTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task SaveMetadata_ShouldAddNewMetadata_WhenMetadataIsNew()
    {
        MetadataService service = new(DbContext);
        FileMetadata metadata = new("path1", FileType.Image, "some desc.");

        FileMetadata savedMetadata = await service.SaveMetadata(metadata);
        FileMetadata metadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1u, savedMetadata.Id);
        Assert.AreNotEqual(default, savedMetadata.UploadedOn);
        Assert.AreEqual("path1", savedMetadata.FileName);
        Assert.AreEqual(FileType.Image, savedMetadata.FileType);
        Assert.AreEqual(FileType.Image, savedMetadata.FileType);
        Assert.AreEqual("some desc.", savedMetadata.Description);
        Assert.AreEqual(0, savedMetadata.Tags.Count);

        Assert.AreEqual(1u, metadataFromDb.Id);
        Assert.AreNotEqual(default, metadataFromDb.UploadedOn);
        Assert.AreEqual("path1", metadataFromDb.FileName);
        Assert.AreEqual(FileType.Image, metadataFromDb.FileType);
        Assert.AreEqual(FileType.Image, metadataFromDb.FileType);
        Assert.AreEqual("some desc.", metadataFromDb.Description);
        Assert.AreEqual(0, metadataFromDb.Tags.Count);
    }

    [TestMethod]
    public async Task SaveMetadata_ShouldUpdateExistingMetadata_WhenMetadataIsNotNew()
    {
        MetadataService service = new(DbContext);
        await service.SaveMetadata(new("path1", FileType.Image, "some desc."));

        FileMetadata metadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);
        metadataFromDb.UpdateDescription("new desc.");
        FileMetadata updatedMetadata = await service.SaveMetadata(metadataFromDb);
        FileMetadata updatedMetadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1u, updatedMetadata.Id);
        Assert.AreEqual(metadataFromDb.UploadedOn, updatedMetadata.UploadedOn);
        Assert.AreEqual("path1", updatedMetadata.FileName);
        Assert.AreEqual(FileType.Image, updatedMetadata.FileType);
        Assert.AreEqual("new desc.", updatedMetadata.Description);
        Assert.AreEqual(0, updatedMetadata.Tags.Count);

        Assert.AreEqual(1u, updatedMetadataFromDb.Id);
        Assert.AreEqual(metadataFromDb.UploadedOn, updatedMetadataFromDb.UploadedOn);
        Assert.AreEqual("path1", updatedMetadataFromDb.FileName);
        Assert.AreEqual(FileType.Image, updatedMetadataFromDb.FileType);
        Assert.AreEqual("new desc.", updatedMetadataFromDb.Description);
        Assert.AreEqual(0, updatedMetadataFromDb.Tags.Count);
    }

    [TestMethod]
    public async Task GetMetadata_ShouldReturnMetadata_WhenMetadataExists()
    {
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, "some desc."));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        FileMetadata metadata = await service.GetMetadata(1u);

        Assert.AreEqual(1u, metadata.Id);
        Assert.AreNotEqual(default, metadata.UploadedOn);
        Assert.AreEqual("path1", metadata.FileName);
        Assert.AreEqual(FileType.Image, metadata.FileType);
        Assert.AreEqual("some desc.", metadata.Description);
    }

    [TestMethod]
    public async Task GetLastMetadataItems_ShouldReturnLastMetadataItems_WhenItemsExist()
    {
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, "some desc."));
        await Task.Delay(50);
        DbContext.FilesMetadata.Add(new("path2", FileType.Image, "some desc."));
        await Task.Delay(50);
        DbContext.FilesMetadata.Add(new("path3", FileType.Image, "some desc."));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        List<FileMetadata> metadataList1 = await service.GetLastMetadataItems(2);
        List<FileMetadata> metadataList2 = await service.GetLastMetadataItems(5);

        Assert.AreEqual(2, metadataList1.Count);
        Assert.AreEqual("path3", metadataList1[0].FileName);
        Assert.AreEqual("path2", metadataList1[1].FileName);

        Assert.AreEqual(3, metadataList2.Count);
        Assert.AreEqual("path3", metadataList2[0].FileName);
        Assert.AreEqual("path2", metadataList2[1].FileName);
        Assert.AreEqual("path1", metadataList2[2].FileName);
    }
}