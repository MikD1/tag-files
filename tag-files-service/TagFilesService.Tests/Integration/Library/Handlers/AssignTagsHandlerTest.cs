using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class AssignTagsHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task AssignTagsRequest_ShouldAssignTagsToOneItem_WhenOneItemPassedAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.LibraryItems.Add(new("path1", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        AssignTagsHandler handler = new(DbContext);
        AssignTagsRequest request = new([1u], ["tag3", "tag1"]);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        LibraryItem itemFromDb = await DbContext.LibraryItems
            .Include(x => x.Tags)
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1, items.Count);
        Assert.AreEqual(2, items[0].Tags.Count);
        Assert.AreEqual(2, itemFromDb.Tags.Count);
        Assert.AreEqual("tag1", itemFromDb.Tags[0].Name);
        Assert.AreEqual("tag3", itemFromDb.Tags[1].Name);
    }

    [TestMethod]
    public async Task AssignTagsRequest_ShouldAssignTagsToAllItems_WhenSeveralItemsPassedAndTagsExist()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag2"));
        DbContext.Tags.Add(new("tag3"));
        DbContext.LibraryItems.Add(new("path1", FileType.Unknown, "some desc."));
        DbContext.LibraryItems.Add(new("path2", FileType.Unknown, "some desc."));
        DbContext.LibraryItems.Add(new("path3", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        AssignTagsHandler handler = new(DbContext);
        AssignTagsRequest request = new([1u, 2u, 3u], ["tag3", "tag1", "tag2"]);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        List<LibraryItem> itemFromDb = await DbContext.LibraryItems
            .Where(x => x.Id == 1u || x.Id == 2u || x.Id == 3u)
            .Include(x => x.Tags)
            .ToListAsync();

        Assert.AreEqual(3, items.Count);
        Assert.AreEqual(3, items[0].Tags.Count);
        Assert.AreEqual(3, itemFromDb.Count);
        Assert.AreEqual(3, itemFromDb[0].Tags.Count);
        Assert.AreEqual("tag1", itemFromDb[0].Tags[0].Name);
        Assert.AreEqual("tag2", itemFromDb[0].Tags[1].Name);
        Assert.AreEqual("tag3", itemFromDb[0].Tags[2].Name);
        Assert.AreEqual("tag1", itemFromDb[1].Tags[0].Name);
        Assert.AreEqual("tag2", itemFromDb[1].Tags[1].Name);
        Assert.AreEqual("tag3", itemFromDb[1].Tags[2].Name);
        Assert.AreEqual("tag1", itemFromDb[2].Tags[0].Name);
        Assert.AreEqual("tag2", itemFromDb[2].Tags[1].Name);
        Assert.AreEqual("tag3", itemFromDb[2].Tags[2].Name);
    }
}