using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class SearchRequestHandlerSortingTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task Search_ShouldSortByUploadedAsc_WhenRequested()
    {
        DbContext.LibraryItems.Add(new("file1", FileType.Image, null));
        await Task.Delay(5);
        DbContext.LibraryItems.Add(new("file2", FileType.Image, null));
        await Task.Delay(5);
        DbContext.LibraryItems.Add(new("file3", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, null, 1, 20, SortType.UploadedAsc);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file1", result.Items[0].Path);
        Assert.AreEqual("library/file2", result.Items[1].Path);
        Assert.AreEqual("library/file3", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldSortByVideoDurationAsc_WhenRequested()
    {
        DbContext.LibraryItems.AddRange(
            new("file1", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(30) },
            new("file2", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(10) },
            new("file3", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(20) });
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, FileType.Video, 1, 20, SortType.VideoDurationAsc);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file2", result.Items[0].Path);
        Assert.AreEqual("library/file3", result.Items[1].Path);
        Assert.AreEqual("library/file1", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldSortByVideoDurationDesc_WhenRequested()
    {
        DbContext.LibraryItems.AddRange(
            new("file1", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(30) },
            new("file2", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(10) },
            new("file3", FileType.Video, null) { VideoDuration = TimeSpan.FromSeconds(20) });
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, FileType.Video, 1, 20, SortType.VideoDurationDesc);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file1", result.Items[0].Path);
        Assert.AreEqual("library/file3", result.Items[1].Path);
        Assert.AreEqual("library/file2", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldSortByViewCountAsc_WhenRequested()
    {
        LibraryItem item1 = new("file1", FileType.Image, null);
        item1.IncrementViewCount(); // 1
        LibraryItem item2 = new("file2", FileType.Image, null);
        LibraryItem item3 = new("file3", FileType.Image, null);
        item3.IncrementViewCount();
        item3.IncrementViewCount(); // 2
        DbContext.LibraryItems.AddRange(item1, item2, item3);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, FileType.Image, 1, 20, SortType.ViewCountAsc);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file2", result.Items[0].Path);
        Assert.AreEqual("library/file1", result.Items[1].Path);
        Assert.AreEqual("library/file3", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldSortByViewCountDesc_WhenRequested()
    {
        LibraryItem item1 = new("file1", FileType.Image, null);
        item1.IncrementViewCount(); // 1
        LibraryItem item2 = new("file2", FileType.Image, null);
        LibraryItem item3 = new("file3", FileType.Image, null);
        item3.IncrementViewCount();
        item3.IncrementViewCount(); // 2
        DbContext.LibraryItems.AddRange(item1, item2, item3);
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, FileType.Image, 1, 20, SortType.ViewCountDesc);
        PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);

        Assert.AreEqual(3, result.Items.Count);
        Assert.AreEqual("library/file3", result.Items[0].Path);
        Assert.AreEqual("library/file1", result.Items[1].Path);
        Assert.AreEqual("library/file2", result.Items[2].Path);
    }

    [TestMethod]
    public async Task Search_ShouldRandomizeOrder_WhenRequested()
    {
        DbContext.LibraryItems.AddRange(
            new("file1", FileType.Image, null),
            new("file2", FileType.Image, null),
            new("file3", FileType.Image, null),
            new("file4", FileType.Image, null),
            new("file5", FileType.Image, null));
        await DbContext.SaveChangesAsync();

        SearchRequestHandler handler = new(DbContext);
        SearchRequest request = new(null, FileType.Image, 1, 20, SortType.Random);

        HashSet<string> distinctOrders = [];
        for (int i = 0; i < 6; i++)
        {
            PaginatedList<LibraryItemDto> result = await handler.Handle(request, CancellationToken.None);
            string orderKey = string.Join(",", result.Items.Select(x => x.Path));
            distinctOrders.Add(orderKey);
        }

        Assert.IsTrue(distinctOrders.Count >= 3, "Random sort did not produce varying orders.");
    }
}