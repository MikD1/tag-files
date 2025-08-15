using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class IncrementViewCountHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task IncrementViewCountRequest_ShouldIncrementCounter()
    {
        DbContext.LibraryItems.Add(new("path1", FileType.Unknown, "some desc."));
        await DbContext.SaveChangesAsync();

        LibraryItem itemFromDb = await DbContext.LibraryItems
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(0, itemFromDb.ViewCount);
        Assert.IsNull(itemFromDb.LastView);

        IncrementViewCountHandler handler = new(DbContext);
        IncrementViewCountRequest request = new(1u);
        await handler.Handle(request, CancellationToken.None);
        itemFromDb = await DbContext.LibraryItems
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(1, itemFromDb.ViewCount);
        Assert.IsNotNull(itemFromDb.LastView);

        DateTime lastView = itemFromDb.LastView.Value;
        await handler.Handle(request, CancellationToken.None);
        await handler.Handle(request, CancellationToken.None);
        await handler.Handle(request, CancellationToken.None);

        itemFromDb = await DbContext.LibraryItems
            .FirstAsync(x => x.Id == 1u);

        Assert.AreEqual(4, itemFromDb.ViewCount);
        Assert.IsNotNull(itemFromDb.LastView);
        Assert.IsTrue(itemFromDb.LastView.Value > lastView);
    }
}