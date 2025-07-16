using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class AssignItemsToCollectionHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task AssignItemsToCollection_ShouldAssignCollectionToOneItem_WhenOneItemPassedAndCollectionExists()
    {
        LibraryCollection collection = new("Collection1");
        DbContext.Collections.Add(collection);
        DbContext.LibraryItems.Add(new("path1", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        AssignItemsToCollectionHandler handler = new(DbContext);
        AssignItemsToCollectionRequest request = new([1u], 1u);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        LibraryItem itemFromDb = await DbContext.LibraryItems.FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1, items.Count);
        Assert.AreEqual(1u, items[0].CollectionId);
        Assert.AreEqual(1u, itemFromDb.CollectionId);
    }

    [TestMethod]
    public async Task
        AssignItemsToCollection_ShouldAssignCollectionToAllItems_WhenSeveralItemsPassedAndCollectionExists()
    {
        LibraryCollection collection = new("Collection1");
        DbContext.Collections.Add(collection);
        DbContext.LibraryItems.Add(new("path1", FileType.Unknown, "some desc."));
        DbContext.LibraryItems.Add(new("path2", FileType.Unknown, "some desc."));
        DbContext.LibraryItems.Add(new("path3", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        AssignItemsToCollectionHandler handler = new(DbContext);
        AssignItemsToCollectionRequest request = new([1u, 2u, 3u], 1u);
        List<LibraryItemDto> items = await handler.Handle(request, CancellationToken.None);
        List<LibraryItem> itemsFromDb = await DbContext.LibraryItems.ToListAsync();

        Assert.AreEqual(3, items.Count);
        Assert.IsTrue(items.All(i => i.CollectionId == 1u));
        Assert.IsTrue(itemsFromDb.All(i => i.CollectionId == 1u));
    }
}