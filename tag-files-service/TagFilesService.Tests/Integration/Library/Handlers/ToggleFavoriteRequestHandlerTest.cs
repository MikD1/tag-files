using Microsoft.EntityFrameworkCore;
using TagFilesService.Library.Contracts;
using TagFilesService.Library.Handlers;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library.Handlers;

[TestClass]
public class ToggleFavoriteRequestHandlerTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task Handle_ShouldToggleFavoriteStatus()
    {
        LibraryItem libraryItem = new("test-file.jpg", FileType.Image, null);
        await DbContext.LibraryItems.AddAsync(libraryItem);
        await DbContext.SaveChangesAsync();

        ToggleFavoriteRequestHandler handler = new(DbContext);
        ToggleFavoriteRequest request = new(libraryItem.Id);
        await handler.Handle(request, CancellationToken.None);
        LibraryItem result = await DbContext.LibraryItems.FirstAsync(x => x.Id == libraryItem.Id);

        Assert.IsTrue(result.IsFavorite);

        await handler.Handle(request, CancellationToken.None);
        result = await DbContext.LibraryItems.FirstAsync(x => x.Id == libraryItem.Id);

        Assert.IsFalse(result.IsFavorite);
    }
}