using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class AssignTagsHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task AssignTagsRequest_ShouldAssignTagsToMetadata_WhenMetadataAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.FilesMetadata.Add(new("path1", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        AssignTagsHandler handler = new(DbContext);
        LibraryItemDto libraryItem = await handler.Handle(new(1u, ["tag1", "tag3"]), CancellationToken.None);
        FileMetadata metadataFromDb = await DbContext.FilesMetadata
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(2, libraryItem.Tags.Count);
        Assert.AreEqual(2, metadataFromDb.Tags.Count);
    }
}