using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Data;

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
        Assert.AreEqual(FileType.Image, savedMetadata.Type);
        Assert.AreEqual("some desc.", savedMetadata.Description);
        Assert.AreEqual(0, savedMetadata.Tags.Count);

        Assert.AreEqual(1u, metadataFromDb.Id);
        Assert.AreNotEqual(default, metadataFromDb.UploadedOn);
        Assert.AreEqual("path1", metadataFromDb.FileName);
        Assert.AreEqual(FileType.Image, metadataFromDb.Type);
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
        Assert.AreEqual(FileType.Image, updatedMetadata.Type);
        Assert.AreEqual("new desc.", updatedMetadata.Description);
        Assert.AreEqual(0, updatedMetadata.Tags.Count);

        Assert.AreEqual(1u, updatedMetadataFromDb.Id);
        Assert.AreEqual(metadataFromDb.UploadedOn, updatedMetadataFromDb.UploadedOn);
        Assert.AreEqual("path1", updatedMetadataFromDb.FileName);
        Assert.AreEqual(FileType.Image, updatedMetadataFromDb.Type);
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
        Assert.AreEqual(FileType.Image, metadata.Type);
        Assert.AreEqual("some desc.", metadata.Description);
    }

    [TestMethod]
    public async Task GetLastMetadataItems_ShouldReturnLastMetadataItems_WhenItemsExist()
    {
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, "some desc."));
        await Task.Delay(50);
        DbContext.FilesMetadata.Add(new("path2", FileType.Video, "some desc."));
        await Task.Delay(50);
        DbContext.FilesMetadata.Add(new("path3", FileType.Audio, "some desc."));
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

    [TestMethod]
    public async Task AssignTags_ShouldAssignTagsToMetadata_WhenMetadataAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, "some desc."));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        FileMetadata metadata = await service.AssignTags(1u, [1u, 2u]);
        FileMetadata metadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(2, metadata.Tags.Count);
        Assert.AreEqual(2, metadataFromDb.Tags.Count);
    }

    [TestMethod]
    public async Task Search_ShouldReturnCorrectMetadata()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.Tags.Add(new("tag4"));
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("path2", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("path3", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        await service.AssignTags(1u, [1u, 2u]);
        await service.AssignTags(2u, [1u, 3u]);
        await service.AssignTags(1u, [1u, 2u, 3u]);

        IPaginatedList<FileMetadata> result = await service.Search("tag1 && (tag2 || tag3) && !tag4", 1, 20);

        Assert.AreEqual(2, result.Items.Count);
        Assert.AreEqual("path2", result.Items[0].FileName);
        Assert.AreEqual("path1", result.Items[1].FileName);
    }

    [TestMethod]
    public async Task Search_ShouldReturnLastMetadata_WhenEmptyQuery()
    {
        DbContext.FilesMetadata.Add(new("path1", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("path2", FileType.Image, null));
        DbContext.FilesMetadata.Add(new("path3", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        MetadataService service = new(DbContext);
        IPaginatedList<FileMetadata> result = await service.Search(string.Empty, 1, 20);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("path3", result.Items[0].FileName);
        Assert.AreEqual("path2", result.Items[1].FileName);
        Assert.AreEqual("path1", result.Items[2].FileName);
    }
}