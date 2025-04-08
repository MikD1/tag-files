using TagFilesService.Infrastructure;

namespace TagFilesService.Tests.Unit.Infrastructure
{
    [TestClass]
    public class PaginatedListTest
    {
        [DataTestMethod]
        [DataRow(1, 10, 50, 5, new[] { 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 })]
        [DataRow(2, 10, 50, 5, new[] { 11, 12, 13, 14, 15, 16, 17, 18, 19, 20 })]
        [DataRow(5, 10, 50, 5, new[] { 41, 42, 43, 44, 45, 46, 47, 48, 49, 50 })]
        [DataRow(1, 3, 50, 17, new[] { 1, 2, 3 })]
        [DataRow(17, 3, 50, 17, new[] { 49, 50 })]
        [DataRow(1, 20, 4, 1, new[] { 1, 2, 3, 4 })]
        public void CreateAsync_ShouldReturnCorrectPagination(int pageIndex, int pageSize, int totalItems, int totalPages,
            int[] expectedItems)
        {
            IQueryable<int> source = Enumerable.Range(1, totalItems).AsQueryable();

            PaginatedList<int> paginatedList = PaginatedList<int>.Create(source, pageIndex, pageSize);

            Assert.AreEqual(expectedItems.Length, paginatedList.Items.Count());
            Assert.AreEqual(totalItems, paginatedList.TotalItems);
            Assert.AreEqual(pageIndex, paginatedList.PageIndex);
            Assert.AreEqual(totalPages, paginatedList.TotalPages);
            CollectionAssert.AreEqual(expectedItems.ToList(), paginatedList.Items.ToList());
        }

        [TestMethod]
        public void CreateAsync_ShouldHandleEmptySource()
        {
            IQueryable<int> source = Enumerable.Empty<int>().AsQueryable();
            int pageIndex = 1;
            int pageSize = 10;

            PaginatedList<int> paginatedList = PaginatedList<int>.Create(source, pageIndex, pageSize);

            Assert.AreEqual(0, paginatedList.Items.Count());
            Assert.AreEqual(0, paginatedList.TotalItems);
            Assert.AreEqual(1, paginatedList.PageIndex);
            Assert.AreEqual(0, paginatedList.TotalPages);
        }
    }
}