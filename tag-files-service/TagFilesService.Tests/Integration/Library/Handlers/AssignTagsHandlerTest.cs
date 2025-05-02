using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class AssignTagsHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task AssignTagsRequest_ShouldAssignTagsToOneMetadata_WhenOneMetadataPassedAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.FilesMetadata.Add(new("path1", "unknown/unknown", "some desc."));
        await DbContext.SaveChangesAsync();

        AssignTagsHandler handler = new(DbContext);
        AssignTagsRequest request = new([1u], ["tag3", "tag1"]);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        FileMetadata metadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1, items.Count);
        Assert.AreEqual(2, items[0].Tags.Count);
        Assert.AreEqual(2, metadataFromDb.Tags.Count);
        Assert.AreEqual("tag1", metadataFromDb.Tags[0].Name);
        Assert.AreEqual("tag3", metadataFromDb.Tags[1].Name);
    }

    [TestMethod]
    public async Task AssignTagsRequest_ShouldAssignTagsToAllMetadata_WhenSeveralMetadataPassedAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.FilesMetadata.Add(new("path1", "unknown/unknown", "some desc."));
        DbContext.FilesMetadata.Add(new("path2", "unknown/unknown", "some desc."));
        DbContext.FilesMetadata.Add(new("path3", "unknown/unknown", "some desc."));
        await DbContext.SaveChangesAsync();

        AssignTagsHandler handler = new(DbContext);
        AssignTagsRequest request = new([1u, 2u, 3u], ["tag3", "tag1", "tag2"]);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        List<FileMetadata> metadataFromDb = await DbContext.FilesMetadata
            .Where(x => x.Id == 1u || x.Id == 2u || x.Id == 3u)
            .Include(x => x.Tags)
            .ToListAsync();

        Assert.AreEqual(3, items.Count);
        Assert.AreEqual(3, items[0].Tags.Count);
        Assert.AreEqual(3, metadataFromDb.Count);
        Assert.AreEqual(3, metadataFromDb[0].Tags.Count);
        Assert.AreEqual("tag1", metadataFromDb[0].Tags[0].Name);
        Assert.AreEqual("tag2", metadataFromDb[0].Tags[1].Name);
        Assert.AreEqual("tag3", metadataFromDb[0].Tags[2].Name);
        Assert.AreEqual("tag1", metadataFromDb[1].Tags[0].Name);
        Assert.AreEqual("tag2", metadataFromDb[1].Tags[1].Name);
        Assert.AreEqual("tag3", metadataFromDb[1].Tags[2].Name);
        Assert.AreEqual("tag1", metadataFromDb[2].Tags[0].Name);
        Assert.AreEqual("tag2", metadataFromDb[2].Tags[1].Name);
        Assert.AreEqual("tag3", metadataFromDb[2].Tags[2].Name);
    }
}